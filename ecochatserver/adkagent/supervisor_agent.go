package adkagent

import (
	"context"
	"fmt"
	"log"
	"strings"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// SupervisorAgent analyzes user requests and creates execution plans
type SupervisorAgent struct {
	llm model.LLM
}

// ExecutionPlan — plan for the support agent
type ExecutionPlan struct {
	Intent       string   // plant_care, device_setup, troubleshooting, mesh_config, app_features, general_info
	Steps        []string
	RequiredTool string
	Keywords     []string
}

// NewSupervisorAgent creates a new supervisor agent
func NewSupervisorAgent(ctx context.Context) (*SupervisorAgent, error) {
	llm, err := NewLLMModel(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM for supervisor: %w", err)
	}

	return &SupervisorAgent{
		llm: llm,
	}, nil
}

// AnalyzeRequest analyzes a user request and creates an execution plan
func (sa *SupervisorAgent) AnalyzeRequest(ctx context.Context, userMessage string) (*ExecutionPlan, error) {
	prompt := fmt.Sprintf(`You are a supervisor agent that analyzes customer requests for Zefir IoT plant monitoring system.

Analyze this customer message and determine the intent:

Customer message: "%s"

INTENTS:

1. "plant_care" - customer asks about a PLANT (care, humidity, species, recommendations)
   Keywords: plant, monstera, aloe, humidity, moisture, watering, care, recommend, compare
   → Plan: Call get_plant_care or search_plant or recommend_plants

2. "device_setup" - customer asks about SETTING UP or CONFIGURING a sensor
   Keywords: setup, pair, connect, bluetooth, wifi, configure, add device, first sensor
   → Plan: Call get_setup_guide

3. "troubleshooting" - customer reports a PROBLEM with sensor or app
   Keywords: not working, offline, won't connect, error, broken, battery drain, wrong reading
   → Plan: Call troubleshoot_device

4. "mesh_config" - customer asks about MESH NETWORK
   Keywords: mesh, ESP-NOW, root, node, topology, range, signal, network
   → Plan: Call get_mesh_info

5. "app_features" - customer asks about APP FEATURES or capabilities
   Keywords: prediction, passport, notification, Home Assistant, map, chart, export
   → Plan: Call get_feature_guide

6. "general_info" - customer asks general questions (FAQ, pricing, contacts, security)
   Keywords: free, price, platform, contact, security, privacy, language, API
   → Plan: Call search_faq or get_app_info or get_contact_info

SENSOR DATA KEYWORDS (if these appear, intent is device-related):
- "my sensor", "my device", "show readings", "check moisture", "show devices"
  → Plan: Call get_user_devices or get_sensor_reading

Respond in this EXACT format:
INTENT: [intent_name]
REQUIRED_TOOL: [first_tool_to_call]
STEPS:
1. [step 1]
2. [step 2]
3. [step 3]
KEYWORDS: [comma-separated keywords]`, userMessage)

	userContent := genai.NewContentFromText(prompt, genai.RoleUser)

	var response strings.Builder
	for llmResp, err := range sa.llm.GenerateContent(ctx, &model.LLMRequest{
		Contents: []*genai.Content{userContent},
	}, false) {
		if err != nil {
			return nil, fmt.Errorf("supervisor LLM error: %w", err)
		}

		if llmResp.Content != nil {
			for _, part := range llmResp.Content.Parts {
				response.WriteString(part.Text)
			}
		}
	}

	plan := sa.parseExecutionPlan(response.String())

	log.Printf("[SUPERVISOR] Analyzed request: intent=%s, required_tool=%s, steps=%d",
		plan.Intent, plan.RequiredTool, len(plan.Steps))

	return plan, nil
}

// parseExecutionPlan parses LLM response into ExecutionPlan
func (sa *SupervisorAgent) parseExecutionPlan(llmResponse string) *ExecutionPlan {
	plan := &ExecutionPlan{
		Steps: []string{},
	}

	lines := strings.Split(llmResponse, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "INTENT:") {
			plan.Intent = strings.TrimSpace(strings.TrimPrefix(line, "INTENT:"))
		} else if strings.HasPrefix(line, "REQUIRED_TOOL:") {
			plan.RequiredTool = strings.TrimSpace(strings.TrimPrefix(line, "REQUIRED_TOOL:"))
		} else if strings.HasPrefix(line, "KEYWORDS:") {
			keywordsStr := strings.TrimSpace(strings.TrimPrefix(line, "KEYWORDS:"))
			plan.Keywords = strings.Split(keywordsStr, ",")
			for i := range plan.Keywords {
				plan.Keywords[i] = strings.TrimSpace(plan.Keywords[i])
			}
		} else if len(line) > 3 && (line[0] >= '1' && line[0] <= '9') && line[1] == '.' {
			step := strings.TrimSpace(line[3:])
			if step != "" {
				plan.Steps = append(plan.Steps, step)
			}
		}
	}

	return plan
}

// ValidateExecution checks that the plan was executed correctly
func (sa *SupervisorAgent) ValidateExecution(plan *ExecutionPlan, toolsCalled []string, result string) bool {
	if plan.RequiredTool != "" {
		found := false
		for _, t := range toolsCalled {
			if t == plan.RequiredTool {
				found = true
				break
			}
		}

		if !found {
			log.Printf("[SUPERVISOR] Validation failed: required tool '%s' was not called", plan.RequiredTool)
			return false
		}
	}

	if len(result) < 10 {
		log.Printf("[SUPERVISOR] Validation failed: result too short")
		return false
	}

	log.Printf("[SUPERVISOR] Validation passed")
	return true
}
