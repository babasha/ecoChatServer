package llm

import (
	"net/http"
	"os"
	"time"
)

// GeminiClient представляет клиента для взаимодействия с Google Gemini API
type GeminiClient struct {
	apiKey string
	client *http.Client
}

// GeminiMessage представляет сообщение в формате Gemini
type GeminiMessage struct {
	Role  string                   `json:"role"`
	Parts []map[string]interface{} `json:"parts"`
}

// GeminiRequest описывает тело POST‑запроса к Gemini API
type GeminiRequest struct {
	Contents          []GeminiMessage          `json:"contents"`
	SystemInstruction *GeminiSystemInstruction `json:"system_instruction,omitempty"`
	GenerationConfig  map[string]interface{}   `json:"generationConfig,omitempty"`
	Tools             []GeminiTool             `json:"tools,omitempty"`
	ToolConfig        *GeminiToolConfig        `json:"toolConfig,omitempty"`
}

// GeminiToolConfig конфигурация для function calling
type GeminiToolConfig struct {
	FunctionCallingConfig GeminiFunctionCallingConfig `json:"functionCallingConfig"`
}

// GeminiFunctionCallingConfig режим вызова функций
type GeminiFunctionCallingConfig struct {
	Mode                 string   `json:"mode"`                           // "AUTO", "ANY", "NONE"
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"` // Опционально
}

// GeminiSystemInstruction представляет системную инструкцию
type GeminiSystemInstruction struct {
	Parts []map[string]interface{} `json:"parts"`
}

// GeminiTool описывает инструмент (функцию) доступный для LLM
type GeminiTool struct {
	FunctionDeclarations []GeminiFunctionDeclaration `json:"functionDeclarations"`
}

// GeminiFunctionDeclaration описывает одну функцию
type GeminiFunctionDeclaration struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// GeminiFunctionCall представляет вызов функции от LLM
type GeminiFunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

// GeminiThoughtContent содержит текст с подписью мыслей (Gemini 2.0 thinking mode)
type GeminiThoughtContent struct {
	ThoughtSignature string `json:"thoughtSignature"`
	Text             string `json:"text"`
}

// GeminiCandidate представляет один из вариантов ответа
type GeminiCandidate struct {
	Content        GeminiMessage         `json:"content"`
	ThoughtContent *GeminiThoughtContent `json:"thoughtContent,omitempty"`
	FunctionCall   *GeminiFunctionCall   `json:"functionCall,omitempty"`
	FinishReason   string                `json:"finishReason"`
}

// GeminiResponse описывает ответ Gemini API
type GeminiResponse struct {
	Candidates     []GeminiCandidate `json:"candidates"`
	PromptFeedback *struct {
		BlockReason   string `json:"blockReason,omitempty"`
		SafetyRatings []struct {
			Category    string `json:"category"`
			Probability string `json:"probability"`
		} `json:"safetyRatings,omitempty"`
	} `json:"promptFeedback,omitempty"`
	UsageMetadata *struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata,omitempty"`
}

// NewGeminiClient создаёт новый GeminiClient
func NewGeminiClient() *GeminiClient {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		panic("GEMINI_API_KEY not set")
	}

	timeout := 30 * time.Second
	if t := os.Getenv("LLM_API_TIMEOUT"); t != "" {
		if d, err := time.ParseDuration(t); err == nil {
			timeout = d
		}
	}

	return &GeminiClient{
		apiKey: apiKey,
		client: &http.Client{Timeout: timeout},
	}
}
