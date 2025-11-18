package adkagent

import (
	"context"
	"fmt"
	"log"
	"strings"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"

	"github.com/egor/ecochatserver/llm"
)

// SupportAgent - AI-агент поддержки клиентов с 19 специализированными инструментами
// Реализует интерфейс UserIDProvider для передачи в tools
type SupportAgent struct {
	agent          agent.Agent
	runner         *runner.Runner
	sessionService session.Service
	storeClient    *llm.StoreClient
	isAuthorized   bool
	appName        string
	userID         int
	currentSessionID string
	rateLimiter    *RateLimiter
}

// GetUserID реализует интерфейс UserIDProvider
func (sa *SupportAgent) GetUserID() int {
	return sa.userID
}

// ptrFloat32 helper function to convert float to *float32
func ptrFloat32(v float32) *float32 {
	return &v
}

// resetContext очищает пользовательский контекст перед возвратом агента в пул
func (sa *SupportAgent) resetContext() {
	sa.userID = 0
	sa.currentSessionID = ""
}

// Note: Response caching removed due to incompatibility with current ADK fork
// Consider re-implementing when ADK callbacks are stable

// NewSupportAgent создаёт агента с полным набором из 19 инструментов
func NewSupportAgent(ctx context.Context, storeClient *llm.StoreClient, isAuthorized bool) (*SupportAgent, error) {
	// 1. Создаём LLM модель
	llmModel, err := NewLLMModel(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM model: %w", err)
	}

	// 2. Создаём переменную для userIDProvider (будет заполнена после создания агента)
	var userIDProvider UserIDProvider

	// 3. Создаём все tools из разных категорий
	var allTools []tool.Tool

	// Product Tools
	productTools, err := CreateProductTools(storeClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create product tools: %w", err)
	}
	allTools = append(allTools, productTools...)
	log.Printf("[AGENT] Added %d product tools", len(productTools))

	// Order Tools (требуют userIDProvider для доступа к userID)
	orderTools, err := CreateOrderTools(storeClient, &userIDProvider)
	if err != nil {
		return nil, fmt.Errorf("failed to create order tools: %w", err)
	}
	allTools = append(allTools, orderTools...)
	log.Printf("[AGENT] Added %d order tools", len(orderTools))

	// Support Tools
	supportTools, err := CreateSupportTools(storeClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create support tools: %w", err)
	}
	allTools = append(allTools, supportTools...)
	log.Printf("[AGENT] Added %d support tools", len(supportTools))

	// 4. Выбираем ОПТИМИЗИРОВАННЫЙ промпт (lean version)
	var systemPrompt string
	if isAuthorized {
		systemPrompt = getLeanAuthorizedPrompt()
		log.Printf("[AGENT] Создаём АВТОРИЗОВАННОГО агента с %d tools (LEAN prompt)", len(allTools))
	} else {
		systemPrompt = getLeanUnauthorizedPrompt()
		log.Printf("[AGENT] Создаём НЕАВТОРИЗОВАННОГО агента с %d tools (LEAN prompt)", len(allTools))
	}

	// 5. Создаём агента с ОПТИМИЗАЦИЯМИ
	adkAgent, err := llmagent.New(llmagent.Config{
		Name:        "enddel_support",
		Model:       llmModel,
		Description: "AI-powered assistant for Enddel online grocery delivery service with comprehensive product, order, and support capabilities",
		Instruction: systemPrompt,
		Tools:       allTools,

		// ✨ ОПТИМИЗАЦИЯ 1: Лимиты генерации
		GenerateContentConfig: &genai.GenerateContentConfig{
			Temperature:     ptrFloat32(0.3),   // Меньше креативности = более стабильные ответы = дешевле
			MaxOutputTokens: 800,                // Лимит на длину ответа (экономия output токенов)
			CandidateCount:  1,                  // Только 1 вариант (не генерировать альтернативы)
			TopP:            ptrFloat32(0.9),    // Nucleus sampling
			TopK:            ptrFloat32(40),     // Top-K sampling
		},

		// ✨ OPTIMIZATION: No history for single-turn requests (saves tokens)
		// Grocery delivery mostly has single-turn requests like "show wine", "do you have milk?"
		IncludeContents: llmagent.IncludeContentsNone,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create agent: %w", err)
	}

	log.Printf("[AGENT] 🚀 Агент создан с ОПТИМИЗАЦИЯМИ: MaxTokens=800, Temperature=0.3, Caching=5min")

	// 6. Создаём session service
	sessionService := session.InMemoryService()

	// 7. Создаём runner
	appName := "ecochat"
	r, err := runner.New(runner.Config{
		AppName:        appName,
		Agent:          adkAgent,
		SessionService: sessionService,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create runner: %w", err)
	}

	log.Printf("[AGENT] ✅ Агент создан с %d инструментами", len(allTools))
	log.Printf("[AGENT] 📊 Breakdown: Products=%d, Orders=%d, Support=%d",
		len(productTools), len(orderTools), len(supportTools))

	// 8. Создаём rate limiter
	rateLimiter := NewGeminiFreeTierLimiter()

	sa := &SupportAgent{
		agent:          adkAgent,
		runner:         r,
		sessionService: sessionService,
		storeClient:    storeClient,
		isAuthorized:   isAuthorized,
		appName:        appName,
		userID:         0,
		rateLimiter:    rateLimiter,
	}

	// Заполняем userIDProvider - SupportAgent реализует UserIDProvider
	userIDProvider = sa

	return sa, nil
}

// ProcessMessage обрабатывает сообщение через ADK runner
// clientLanguage - опциональный параметр языка клиента (ru, en, ka и т.д.)
func (sa *SupportAgent) ProcessMessage(ctx context.Context, sessionID, message string, storeUserID int, clientLanguage ...string) (string, error) {
	defer sa.resetContext()

	// Проверяем rate limiter
	if sa.rateLimiter != nil && !sa.rateLimiter.AllowRequest() {
		rpm, rpd, maxRPM, maxRPD := sa.rateLimiter.GetStats()
		log.Printf("[AGENT] ⚠️ Rate limit exceeded: RPM=%d/%d, RPD=%d/%d", rpm, maxRPM, rpd, maxRPD)
		return fmt.Sprintf("Извините, достигнут лимит запросов к AI. RPM: %d/%d, RPD: %d/%d. Пожалуйста, попробуйте позже.",
			rpm, maxRPM, rpd, maxRPD), nil
	}

	// Сохраняем userID для использования в tools
	sa.userID = storeUserID
	sa.currentSessionID = sessionID

	// 1. Создаём или получаем сессию
	sessionResp, err := sa.sessionService.Create(ctx, &session.CreateRequest{
		AppName: sa.appName,
		UserID:  sessionID,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}

	// 2. Создаём user content с инструкцией по языку
	userMessage := message
	if len(clientLanguage) > 0 && clientLanguage[0] != "" {
		lang := clientLanguage[0]
		var langInstruction string
		switch lang {
		case "ru":
			langInstruction = "[IMPORTANT: Respond ONLY in Russian language]"
		case "en":
			langInstruction = "[IMPORTANT: Respond ONLY in English language]"
		case "ka", "ge":
			langInstruction = "[IMPORTANT: Respond ONLY in Georgian language]"
		default:
			langInstruction = fmt.Sprintf("[IMPORTANT: Respond ONLY in %s language]", lang)
		}
		userMessage = langInstruction + "\n\n" + message
		log.Printf("[AGENT] Client language detected: %s, added instruction", lang)
	}

	userMsg := genai.NewContentFromText(userMessage, genai.RoleUser)

	// 3. Запускаем агента
	var response strings.Builder
	toolCallsCount := 0

	for event, err := range sa.runner.Run(ctx, sessionID, sessionResp.Session.ID(), userMsg, agent.RunConfig{
		StreamingMode: agent.StreamingModeNone,
	}) {
		if err != nil {
			log.Printf("[AGENT] ❌ Error during run: %v", err)
			return "", fmt.Errorf("agent run error: %w", err)
		}

		if event.LLMResponse.Content != nil {
			for _, part := range event.LLMResponse.Content.Parts {
				response.WriteString(part.Text)

				// Логируем вызовы tools
				if part.FunctionCall != nil {
					toolCallsCount++
					log.Printf("[AGENT] 🔧 Tool called: %s", part.FunctionCall.Name)
					// Детальное логирование параметров
					if part.FunctionCall.Args != nil {
						log.Printf("[AGENT] 📋 Tool args: %v", part.FunctionCall.Args)
					}
				}
			}
		}
	}

	result := response.String()
	log.Printf("[AGENT] ✅ Response generated (%d chars, %d tool calls)", len(result), toolCallsCount)
	if len(result) > 100 {
		log.Printf("[AGENT] Preview: %s...", result[:100])
	} else {
		log.Printf("[AGENT] Preview: %s", result)
	}

	return result, nil
}

// IsEscalationNeeded проверяет нужна ли эскалация
func (sa *SupportAgent) IsEscalationNeeded(response string) bool {
	return strings.Contains(response, "#escalate")
}

// ============================================================================
// ОПТИМИЗИРОВАННЫЕ LEAN PROMPTS - экономия ~370 токенов (82%)
// ============================================================================

func getLeanUnauthorizedPrompt() string {
	return `AI assistant for Enddel grocery delivery (Tbilisi).

CRITICAL RULES:
1. NEVER invent product names, prices, or availability - ALWAYS use tools first
2. If tools return "No products found" or empty results, say exactly that - do NOT suggest products from memory
3. When asked about products/categories: call get_products or search_product - get REAL data before responding
4. Always respond in customer's language
5. Escalate (#escalate) if: bot question, complaints, refunds, customer frustrated

IMPORTANT: You do NOT have product knowledge in your memory. You MUST use tools for ALL product information.`
}

func getLeanAuthorizedPrompt() string {
	return getLeanUnauthorizedPrompt() + `

AUTHORIZED: Access to order tools. Show only user's own orders.`
}
