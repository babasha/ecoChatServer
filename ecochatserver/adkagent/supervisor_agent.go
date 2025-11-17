package adkagent

import (
	"context"
	"fmt"
	"log"
	"strings"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// SupervisorAgent - агент-контролер который анализирует запрос и создает план выполнения
type SupervisorAgent struct {
	llm model.LLM
}

// ExecutionPlan - план выполнения для Support Agent
type ExecutionPlan struct {
	Intent       string   // Что хочет пользователь: "find_category_products", "search_specific_product", etc
	Steps        []string // Четкие шаги выполнения
	RequiredTool string   // Обязательный первый tool
	Keywords     []string // Ключевые слова из запроса
}

// NewSupervisorAgent создает нового supervisor agent
func NewSupervisorAgent(ctx context.Context) (*SupervisorAgent, error) {
	llm, err := NewLLMModel(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM for supervisor: %w", err)
	}

	return &SupervisorAgent{
		llm: llm,
	}, nil
}

// AnalyzeRequest анализирует запрос пользователя и создает план выполнения
func (sa *SupervisorAgent) AnalyzeRequest(ctx context.Context, userMessage string) (*ExecutionPlan, error) {
	prompt := fmt.Sprintf(`You are a supervisor agent that analyzes customer requests and creates execution plans.

Analyze this customer message and determine the intent:

Customer message: "%s"

Identify the intent and create a plan:

INTENTS:
1. "find_category_products" - customer asks about a CATEGORY of products (drinks, food, desserts, etc)
   → Plan: MUST call get_categories first, then get_products_by_category

2. "search_specific_product" - customer asks about a SPECIFIC product by name
   → Plan: Call search_product directly

3. "get_all_products" - customer wants to see all products
   → Plan: Call get_products

4. "check_order" - customer asks about their order
   → Plan: Call get_user_orders or get_order_status

5. "general_info" - customer asks about delivery, payment, store info
   → Plan: Call get_store_info

CATEGORY KEYWORDS (if these appear, intent is "find_category_products"):
- drinks, beverages, напитки, попить
- food, еда, поесть
- wine, вино
- desserts, десерты, сладкое
- salads, салаты
- meat, мясо

Respond in this EXACT format:
INTENT: [intent_name]
REQUIRED_TOOL: [first_tool_to_call]
STEPS:
1. [step 1]
2. [step 2]
3. [step 3]
KEYWORDS: [comma-separated keywords]`, userMessage)

	// Создаем запрос к LLM
	content := genai.NewContentFromText(prompt, genai.RoleUser)

	// Получаем ответ
	var response strings.Builder
	for llmResp, err := range sa.llm.GenerateContent(ctx, &model.LLMRequest{
		Content: []*genai.Content{content},
	}, false) {
		if err != nil {
			return nil, fmt.Errorf("supervisor LLM error: %w", err)
		}

		for _, part := range llmResp.Content.Parts {
			response.WriteString(part.Text)
		}
	}

	// Парсим ответ
	plan := sa.parseExecutionPlan(response.String())

	log.Printf("[SUPERVISOR] Analyzed request: intent=%s, required_tool=%s, steps=%d",
		plan.Intent, plan.RequiredTool, len(plan.Steps))

	return plan, nil
}

// parseExecutionPlan парсит ответ от LLM в структуру ExecutionPlan
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
		} else if len(line) > 0 && (line[0] >= '1' && line[0] <= '9') {
			// Это шаг плана (начинается с цифры)
			step := strings.TrimSpace(line[3:]) // Убираем "1. "
			if step != "" {
				plan.Steps = append(plan.Steps, step)
			}
		}
	}

	return plan
}

// ValidateExecution проверяет что план был выполнен корректно
func (sa *SupervisorAgent) ValidateExecution(plan *ExecutionPlan, toolsCalled []string, result string) bool {
	// Проверяем что обязательный tool был вызван
	if plan.RequiredTool != "" {
		found := false
		for _, tool := range toolsCalled {
			if tool == plan.RequiredTool {
				found = true
				break
			}
		}

		if !found {
			log.Printf("[SUPERVISOR] ❌ Validation failed: required tool '%s' was not called", plan.RequiredTool)
			return false
		}
	}

	// Проверяем что результат не пустой
	if len(result) < 10 {
		log.Printf("[SUPERVISOR] ❌ Validation failed: result too short")
		return false
	}

	log.Printf("[SUPERVISOR] ✅ Validation passed")
	return true
}
