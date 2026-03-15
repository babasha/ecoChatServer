package adkagent

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"log"
	"strings"

	"github.com/egor/ecochatserver/llm"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// ProviderAdapter wraps llm.Provider to implement ADK model.LLM interface.
// This allows the РОП (L1 agent) to use any provider that the Director supports:
// Claude, Ollama, Gemini OAuth, OpenAI OAuth, etc.
type ProviderAdapter struct {
	provider  llm.Provider
	modelName string
}

// Compile-time check: ProviderAdapter implements model.LLM
var _ model.LLM = (*ProviderAdapter)(nil)

// NewProviderAdapter creates an ADK-compatible model from an llm.Provider.
func NewProviderAdapter(provider llm.Provider, modelName string) *ProviderAdapter {
	return &ProviderAdapter{
		provider:  provider,
		modelName: modelName,
	}
}

// Name returns the model identifier.
func (pa *ProviderAdapter) Name() string {
	return pa.modelName
}

// GenerateContent implements model.LLM. It converts ADK request format to llm.Provider
// format, calls the provider, and converts the response back.
func (pa *ProviderAdapter) GenerateContent(
	ctx context.Context,
	req *model.LLMRequest,
	stream bool,
) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		// 1. Extract system prompt from config
		var systemPrompt string
		if req.Config != nil && req.Config.SystemInstruction != nil {
			systemPrompt = extractText(req.Config.SystemInstruction)
		}

		// 2. Convert genai.Content history → llm.Message history + extract last user message
		history, lastUserMsg := convertContentsToMessages(req.Contents)

		// 3. Convert tools
		tools := convertToolsFromADK(req.Tools)

		// 4. Build generation options
		opts := &llm.GenerateOptions{
			SystemPrompt: systemPrompt,
		}
		if req.Config != nil {
			if req.Config.Temperature != nil {
				opts.Temperature = float64(*req.Config.Temperature)
			}
			if req.Config.MaxOutputTokens > 0 {
				opts.MaxTokens = int(req.Config.MaxOutputTokens)
			}
		}

		// 5. Call llm.Provider
		var resp *llm.Response
		var err error

		if len(tools) > 0 {
			resp, err = pa.provider.GenerateWithTools(ctx, lastUserMsg, history, tools, opts)
		} else {
			resp, err = pa.provider.GenerateResponse(ctx, lastUserMsg, history, opts)
		}

		if err != nil {
			yield(nil, fmt.Errorf("provider %s error: %w", pa.provider.GetName(), err))
			return
		}

		// 6. Convert llm.Response → model.LLMResponse
		llmResp := convertResponseToADK(resp)
		yield(llmResp, nil)
	}
}

// ============================================================================
// Conversion: genai.Content[] → llm.Message[] + last user message
// ============================================================================

// convertContentsToMessages converts ADK's genai.Content slice into llm.Message
// history and extracts the last user message text.
// ADK passes the full conversation including tool calls/responses.
func convertContentsToMessages(contents []*genai.Content) (history []llm.Message, lastUserMsg string) {
	if len(contents) == 0 {
		return nil, ""
	}

	for _, content := range contents {
		if content == nil {
			continue
		}

		role := mapGenaiRole(content.Role)

		for _, part := range content.Parts {
			if part == nil {
				continue
			}

			// Text parts
			if part.Text != "" {
				history = append(history, llm.Message{
					Role:    role,
					Content: part.Text,
				})
				continue
			}

			// Function call from assistant → record as assistant message
			if part.FunctionCall != nil {
				argsJSON, _ := json.Marshal(part.FunctionCall.Args)
				history = append(history, llm.Message{
					Role:    "assistant",
					Content: fmt.Sprintf("[tool_call: %s(%s)]", part.FunctionCall.Name, string(argsJSON)),
				})
				continue
			}

			// Function response → record as function message
			if part.FunctionResponse != nil {
				respJSON, _ := json.Marshal(part.FunctionResponse.Response)
				history = append(history, llm.Message{
					Role:    "function",
					Content: string(respJSON),
				})
				continue
			}
		}
	}

	// Extract last user message and trim it from history
	// llm.Provider expects: GenerateWithTools(ctx, userMessage, history, ...)
	// where history does NOT include the current user message
	lastUserMsg = ""
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "user" {
			lastUserMsg = history[i].Content
			history = append(history[:i], history[i+1:]...)
			break
		}
	}

	return history, lastUserMsg
}

// mapGenaiRole converts genai role to llm.Message role.
func mapGenaiRole(genaiRole string) string {
	switch genaiRole {
	case "model":
		return "assistant"
	case "user":
		return "user"
	case "function":
		return "function"
	default:
		return genaiRole
	}
}

// extractText extracts all text from a genai.Content.
func extractText(content *genai.Content) string {
	if content == nil {
		return ""
	}
	var parts []string
	for _, p := range content.Parts {
		if p != nil && p.Text != "" {
			parts = append(parts, p.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// ============================================================================
// Conversion: ADK tools (map[string]any) → llm.Tool[]
// ============================================================================

// convertToolsFromADK converts ADK's tool map into llm.Tool slice.
// ADK tools map supports multiple formats; we handle the common ones.
func convertToolsFromADK(adkTools map[string]any) []llm.Tool {
	if len(adkTools) == 0 {
		return nil
	}

	var tools []llm.Tool

	for name, toolDef := range adkTools {
		tool := llm.Tool{Name: name}

		switch td := toolDef.(type) {
		case map[string]any:
			// Format: {"description": "...", "parameters": {...}}
			if desc, ok := td["description"].(string); ok {
				tool.Description = desc
			}
			if params, ok := td["parameters"].(map[string]interface{}); ok {
				tool.Parameters = params
			}
		default:
			// Try to get description/parameters from interface methods
			if describer, ok := toolDef.(interface{ Description() string }); ok {
				tool.Description = describer.Description()
			}
			if namer, ok := toolDef.(interface{ Name() string }); ok {
				tool.Name = namer.Name()
			}
			// Try to extract declaration (ADK tool pattern)
			if declarer, ok := toolDef.(interface {
				Declaration() *genai.FunctionDeclaration
			}); ok {
				decl := declarer.Declaration()
				if decl != nil {
					tool.Name = decl.Name
					tool.Description = decl.Description
					if decl.Parameters != nil {
						tool.Parameters = schemaToMap(decl.Parameters)
					}
				}
			}
		}

		if tool.Name != "" {
			tools = append(tools, tool)
		}
	}

	return tools
}

// schemaToMap converts genai.Schema to map[string]interface{} for llm.Tool.Parameters.
func schemaToMap(schema *genai.Schema) map[string]interface{} {
	if schema == nil {
		return nil
	}

	result := map[string]interface{}{}

	if schema.Type != "" {
		result["type"] = string(schema.Type)
	}
	if schema.Description != "" {
		result["description"] = schema.Description
	}
	if len(schema.Enum) > 0 {
		result["enum"] = schema.Enum
	}
	if len(schema.Required) > 0 {
		result["required"] = schema.Required
	}

	if len(schema.Properties) > 0 {
		props := map[string]interface{}{}
		for propName, propSchema := range schema.Properties {
			props[propName] = schemaToMap(propSchema)
		}
		result["properties"] = props
	}

	if schema.Items != nil {
		result["items"] = schemaToMap(schema.Items)
	}

	return result
}

// ============================================================================
// Conversion: llm.Response → model.LLMResponse
// ============================================================================

// convertResponseToADK converts llm.Response back to ADK's model.LLMResponse.
func convertResponseToADK(resp *llm.Response) *model.LLMResponse {
	llmResp := &model.LLMResponse{
		TurnComplete: true,
	}

	// Map finish reason
	switch resp.FinishReason {
	case "stop":
		llmResp.FinishReason = genai.FinishReasonStop
	case "length":
		llmResp.FinishReason = genai.FinishReasonMaxTokens
	default:
		llmResp.FinishReason = genai.FinishReasonStop
	}

	// Usage metadata
	if resp.Usage != nil {
		llmResp.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     int32(resp.Usage.PromptTokens),
			CandidatesTokenCount: int32(resp.Usage.CompletionTokens),
			TotalTokenCount:      int32(resp.Usage.TotalTokens),
		}
	}

	// Build content parts
	var parts []*genai.Part

	// Function call response
	if resp.FunctionCall != nil {
		parts = append(parts, &genai.Part{
			FunctionCall: &genai.FunctionCall{
				Name: resp.FunctionCall.Name,
				Args: resp.FunctionCall.Arguments,
			},
		})
		llmResp.FinishReason = genai.FinishReasonStop
	}

	// Text response
	if resp.Text != "" {
		parts = append(parts, &genai.Part{
			Text: resp.Text,
		})
	}

	if len(parts) > 0 {
		llmResp.Content = &genai.Content{
			Parts: parts,
			Role:  "model",
		}
	}

	return llmResp
}

// ============================================================================
// Factory: create ADK model.LLM from RESPONDER_* DB settings
// ============================================================================

// NewModelFromProvider creates an ADK model.LLM by wrapping an existing llm.Provider.
// Use this when you already have a configured provider instance.
func NewModelFromProvider(provider llm.Provider, modelName string) model.LLM {
	log.Printf("[PROVIDER_ADAPTER] Wrapping %s as ADK model (model=%s)", provider.GetName(), modelName)
	return NewProviderAdapter(provider, modelName)
}

// NewModelFromRole creates an ADK model.LLM using the role-based provider system.
// This enables the РОП to use any provider configured for the RESPONDER role
// (Claude, Ollama, Gemini OAuth, etc.) via DB settings.
func NewModelFromRole() (model.LLM, error) {
	provider, err := llm.NewProviderForRole(llm.RoleResponder)
	if err != nil {
		return nil, fmt.Errorf("create provider for RESPONDER role: %w", err)
	}

	modelName := fmt.Sprintf("%s-via-adapter", provider.GetName())
	log.Printf("[PROVIDER_ADAPTER] Created ADK model from RESPONDER role: %s", modelName)
	return NewProviderAdapter(provider, modelName), nil
}
