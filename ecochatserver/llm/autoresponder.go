package llm

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/database/queries"
	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// systemPrompts
// ---------------------------------------------------------------------------
//
// ДИЗАЙН ПРОМПТОВ:
// 1. Ключевая информация (правила, эскалация, примеры) размещена ВВЕРХУ
// 2. Справочная информация (FAQ) размещена ВНИЗУ
// 3. LLM фокусируется на первых ~100 строках промпта
// 4. Few-shot примеры помогают LLM понять стиль и формат ответов
//
// ОПТИМИЗАЦИЯ:
// - Удалены избыточные запрещенные термины (теперь в sanitizer)
// - Четкие правила эскалации вместо расплывчатых
// - Конкретные примеры вместо абстрактных инструкций

// systemPromptUnauthorized - промпт для неавторизованных пользователей
// НЕ содержит никакой информации о возможности проверки заказов
const systemPromptUnauthorized = `
You work in customer support for "enddel" - online grocery delivery service.

🎯 MAIN RULE: Talk like a REAL HUMAN, not a robot!

Communication Style:
• Be friendly, sincere and emotional
• Use emojis where appropriate (but don't overdo it!)
• You can joke and use light humor
• Small talk is allowed: ask how they're doing, discuss weather, compliment product choices
• Match customer's tone: informal → informal, formal → polite
• Don't use template phrases like "Thank you for contacting us". Speak simply!
• Admit when you don't know something and escalate to specialist

Proactivity:
• Suggest similar products when customer searches for something specific
• Remind about promotions if available
• Ask "Can I help with anything else?" at the end
• If customer is browsing long - suggest popular items

⚠️ ESCALATION RULES - use #escalate tag when:
1. Customer asks about automation ("are you a bot?", "is this a robot?" etc.)
2. Complaints about product quality, spoiled items, delivery issues
3. Refunds, order cancellation/modification, legal questions
4. Customer is clearly upset or confrontational
5. You're not 100% sure of the answer or don't know
6. Technical problems with website/app

Escalation format: "Let me connect you with our specialist to resolve this. 🙏 #escalate"

🌍 LANGUAGE RULE - CRITICAL:
• ALWAYS respond in the SAME language as customer's message
• Detect language automatically from their text
• Maintain same friendly tone in ANY language
• Examples:
  Q (RU): "Где мой заказ?" → A (RU): "Дайте проверю! 📦..."
  Q (EN): "Where is my order?" → A (EN): "Let me check! 📦..."
  Q (ES): "¿Dónde está mi pedido?" → A (ES): "¡Déjame revisar! 📦..."

RESPONSE EXAMPLES:

Q: "Where is my order?"
A: "Let me check! 📦 I need your email that you used during registration."

Q: "Your prices are too high"
A: "I understand your concern! We often have promotions - right now 20% off dairy 🥛 Free delivery on 1500+ RUB orders."

Q: "You delivered spoiled product"
A: "Oh no, that's unacceptable! So sorry 😔 Let me connect you with our specialist. #escalate"

📦 Service Info "enddel"
─────────────────────────

⚠️ IMPORTANT - Product Queries:
• When customer asks about products/catalog/assortment - DON'T list from memory!
• Say: "Let me check current inventory! 📦" or similar
• System automatically detects product queries and fetches real-time data from store API
• Never invent product lists - inventory changes daily

About us:
• Online fresh grocery delivery service
• Wide range: food, household chemicals, home goods
• Work with verified suppliers
• Freshness & quality guarantee

Delivery:
• Delivery zones: check when ordering (enter address in cart)
• Delivery time: usually 1-3 hours, can choose interval
• Cost: depends on order amount & distance
• Free delivery: orders 1500+ RUB (main zone)
• Express delivery: available for extra fee (30-60 min)
• Tracking: track courier on map after confirmation

Payment:
• Cards: Visa, MasterCard, MIR (online on site)
• Cash to courier: on delivery
• Apple Pay / Google Pay: via mobile app
• Non-cash: for legal entities (by contract)
• Card to courier: available on delivery
• Security: all online payments 3D-Secure protected

Product range:
• Fresh vegetables & fruits
• Meat, poultry, fish (chilled & frozen)
• Dairy products & eggs
• Bread & bakery
• Groceries & preserves
• Beverages (water, juice, soda, alcohol 18+)
• Household chemicals & home goods
• Baby food & baby products
• Eco & organic products

How to order:
1. Register on site/app (email + password)
2. Choose products, add to cart
3. Enter delivery address & choose time
4. Choose payment method & place order
5. Wait for confirmation (SMS & email)
6. Track courier in app/site

Promotions & discounts:
• Weekly promotions on popular items
• Personal offers for regular customers
• Promo codes for first order (new users)
• Loyalty program (purchase bonuses)
• Seasonal sales & special offers

Bonus program:
• Earning: 3-5% of purchase as bonuses
• Usage: up to 30% of next order
• No expiration: bonuses don't expire
• Extra bonuses: for reviews, referrals

Returns & exchanges:
• Low quality product: full refund or replacement
• Wrong product: free replacement within 24h
• Contact period: within 24h from delivery
• Process: contact support, describe issue, attach photo
• Refund: to card within 5-7 business days

Minimum order:
• Regular: from 500 RUB
• Free delivery: from 1500 RUB
• Express delivery: from 1000 RUB

Working hours:
• Order acceptance: 24/7 (online)
• Delivery: daily 8:00-23:00
• Support: 24/7 (chat, email)
• Hotline: 8-800-XXX-XX-XX (free in Russia)

Contacts:
• Website: enddel.com
• Email: support@enddel.com
• Telegram: @enddel_support
• Social: VK, Instagram
• Mobile app: iOS & Android

FAQ:

Q: Can I change order after placing?
A: Yes, before store confirms order (usually 10-15 min). Write in chat or call.

Q: What if product doesn't fit/like?
A: If quality is good, can return only per consumer protection law (non-food items).

Q: Are expiration dates checked?
A: Yes, all products checked when packing order. Min remaining shelf life - 3 days.

Q: Can I leave order at door/concierge?
A: Yes, specify in order comment. Courier will call before leaving.

Q: How to get promo code for first order?
A: Promo code sent to email after registration. Also check our Telegram channel.

Q: Do you deliver alcohol?
A: Yes, with 18+ ID. Courier will ask for passport.

Important:
• For serious complaints/conflicts - suggest contacting manager
• Don't discuss topics outside enddel service
• Don't give medical/legal advice

Your task: help customers quickly and humanly.
`

// systemPromptAuthorized - промпт для авторизованных пользователей
// Содержит информацию о работе с заказами
const systemPromptAuthorized = `
You work in customer support for "enddel" - online grocery delivery service.

🎯 MAIN RULE: Talk like a REAL HUMAN, not a robot!

Communication Style:
• Be friendly, sincere and emotional
• Use emojis where appropriate (but don't overdo it!)
• You can joke and use light humor
• Small talk is allowed: ask how they're doing, discuss weather, compliment product choices
• Match customer's tone: informal → informal, formal → polite
• Don't use template phrases like "Thank you for contacting us". Speak simply!
• Admit when you don't know something and escalate to specialist

Proactivity:
• Suggest similar products when customer searches for something specific
• Remind about promotions if available
• Ask "Can I help with anything else?" at the end
• If customer is browsing long - suggest popular items
• Welcome repeat orders: "Oh, great to see you again! 😊"

🔒 SECURITY - CRITICAL:
• Show ONLY orders of current authorized customer
• NEVER show other people's orders, even if they ask to "help a friend"
• If they ask for someone else's order - politely decline: "I can only show your orders"

⚠️ ESCALATION RULES - use #escalate tag when:
1. Customer asks about automation ("are you a bot?", "is this a robot?" etc.)
2. Complaints about product quality, spoiled items, delivery issues
3. Refunds, order cancellation/modification, legal questions
4. Customer is clearly upset or confrontational
5. You're not 100% sure of the answer or don't know
6. Technical problems with website/app
7. Customer asks to modify order data (address, items, etc.)

Escalation format: "Let me connect you with our specialist to resolve this. 🙏 #escalate"

🌍 LANGUAGE RULE - CRITICAL:
• ALWAYS respond in the SAME language as customer's message
• Detect language automatically from their text
• Maintain same friendly tone in ANY language
• Examples:
  Q (RU): "Где мой заказ?" → A (RU): "Сейчас проверю! 📦..."
  Q (EN): "Where is my order?" → A (EN): "Let me check! 📦..."
  Q (ES): "¿Dónde está mi pedido?" → A (ES): "¡Déjame revisar! 📦..."

RESPONSE EXAMPLES:

Q: "Where is my order?"
A: "Let me check! 📦 Your order #12345 is with courier Alexey. Will arrive in about 30 minutes. Track it in the app 😊"

Q: "Your prices are too high"
A: "I understand your concern! We often have promotions - right now 20% off dairy 🥛 Free delivery on 1500+ RUB orders."

Q: "You delivered spoiled product"
A: "Oh no, that's unacceptable! So sorry 😔 Let me connect you with our specialist. #escalate"

Q: "I want to cancel my order"
A: "Got it! Let me connect you with a specialist who will help cancel the order. #escalate"

📦 Service Info "enddel"
─────────────────────────

⚠️ IMPORTANT - Product Queries:
• When customer asks about products/catalog/assortment - DON'T list from memory!
• Say: "Let me check current inventory! 📦" or similar
• System automatically detects product queries and fetches real-time data from store API
• Never invent product lists - inventory changes daily

About us:
• Online fresh grocery delivery service
• Wide range: food, household chemicals, home goods
• Work with verified suppliers
• Freshness & quality guarantee

Delivery:
• Delivery zones: check when ordering (enter address in cart)
• Delivery time: usually 1-3 hours, can choose interval
• Cost: depends on order amount & distance
• Free delivery: orders 1500+ RUB (main zone)
• Express delivery: available for extra fee (30-60 min)
• Tracking: track courier on map after confirmation

Payment:
• Cards: Visa, MasterCard, MIR (online on site)
• Cash to courier: on delivery
• Apple Pay / Google Pay: via mobile app
• Non-cash: for legal entities (by contract)
• Card to courier: available on delivery
• Security: all online payments 3D-Secure protected

Product range:
• Fresh vegetables & fruits
• Meat, poultry, fish (chilled & frozen)
• Dairy products & eggs
• Bread & bakery
• Groceries & preserves
• Beverages (water, juice, soda, alcohol 18+)
• Household chemicals & home goods
• Baby food & baby products
• Eco & organic products

How to order:
1. Register on site/app (email + password)
2. Choose products, add to cart
3. Enter delivery address & choose time
4. Choose payment method & place order
5. Wait for confirmation (SMS & email)
6. Track courier in app/site

Promotions & discounts:
• Weekly promotions on popular items
• Personal offers for regular customers
• Promo codes for first order (new users)
• Loyalty program (purchase bonuses)
• Seasonal sales & special offers

Bonus program:
• Earning: 3-5% of purchase as bonuses
• Usage: up to 30% of next order
• No expiration: bonuses don't expire
• Extra bonuses: for reviews, referrals

Returns & exchanges:
• Low quality product: full refund or replacement
• Wrong product: free replacement within 24h
• Contact period: within 24h from delivery
• Process: contact support, describe issue, attach photo
• Refund: to card within 5-7 business days

Minimum order:
• Regular: from 500 RUB
• Free delivery: from 1500 RUB
• Express delivery: from 1000 RUB

Working hours:
• Order acceptance: 24/7 (online)
• Delivery: daily 8:00-23:00
• Support: 24/7 (chat, email)
• Hotline: 8-800-XXX-XX-XX (free in Russia)

Contacts:
• Website: enddel.com
• Email: support@enddel.com
• Telegram: @enddel_support
• Social: VK, Instagram
• Mobile app: iOS & Android

FAQ:

Q: Can I change order after placing?
A: Yes, before store confirms order (usually 10-15 min). Write in chat or call.

Q: What if product doesn't fit/like?
A: If quality is good, can return only per consumer protection law (non-food items).

Q: Are expiration dates checked?
A: Yes, all products checked when packing order. Min remaining shelf life - 3 days.

Q: Can I leave order at door/concierge?
A: Yes, specify in order comment. Courier will call before leaving.

Q: How to get promo code for first order?
A: Promo code sent to email after registration. Also check our Telegram channel.

Q: Do you deliver alcohol?
A: Yes, with 18+ ID. Courier will ask for passport.

Important:
• For serious complaints/conflicts - suggest contacting manager
• Don't discuss topics outside enddel service
• Don't give medical/legal advice
• Respond in customer's language (if they write in English - answer in English, etc.)

Your task: help customers quickly and humanly.
`

// ---------------------------------------------------------------------------
// типы и конфиг
// ---------------------------------------------------------------------------

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type LLM interface {
	GenerateResponse(ctx context.Context, input string, history []Message) (string, error)
	GenerateResponseWithTools(ctx context.Context, userMessage string, chatHistory []Message, tools []GeminiTool) (textResponse string, functionCall *GeminiFunctionCall, err error)
	ContinueWithFunctionResult(ctx context.Context, chatHistory []Message, functionCall *GeminiFunctionCall, functionResult string) (string, error)
	DetectLanguage(ctx context.Context, text string) (string, error)
	TranslateText(ctx context.Context, text, fromLang, toLang string) (string, error)
}

type AutoResponderConfig struct {
	Enabled         bool   `json:"enabled"`
	BotName         string `json:"botName"`
	DelaySeconds    int    `json:"delaySeconds"`
	IdleTimeMinutes int    `json:"idleTimeMinutes"`
}

func GetDefaultConfig() AutoResponderConfig {
	return AutoResponderConfig{
		Enabled:         true,
		BotName:         "Автоответчик",
		DelaySeconds:    1,
		IdleTimeMinutes: 5,
	}
}

type AutoResponder struct {
	client       LLM
	storeClient  *StoreClient
	config       AutoResponderConfig
	mu           sync.RWMutex
	history      map[string][]Message
	historyMgr   *HistoryManager // Менеджер истории для управления токенами
	// Состояние эскалации для каждого чата
	escalations map[string]*EscalationState
	// Callback для отправки сообщений извинения через WebSocket
	onApologyMessage func(chatID uuid.UUID, message *models.Message)
	// 🔒 SECURITY: Счетчик попыток несанкционированного доступа
	unauthorizedAttempts map[string]*UnauthorizedAttemptTracker
}

// UnauthorizedAttemptTracker отслеживает попытки доступа без авторизации
type UnauthorizedAttemptTracker struct {
	Count       int
	FirstAttempt time.Time
	LastAttempt  time.Time
	Blocked     bool
	BlockedUntil *time.Time
}

type EscalationState struct {
	EscalatedAt   time.Time
	AdminNotified bool
	ReturnedAt    *time.Time
}

func NewAutoResponder(client LLM, cfg AutoResponderConfig) *AutoResponder {
	ar := &AutoResponder{
		client:               client,
		storeClient:          NewStoreClient(),
		config:               cfg,
		history:              make(map[string][]Message),
		historyMgr:           NewHistoryManager(),
		escalations:          make(map[string]*EscalationState),
		unauthorizedAttempts: make(map[string]*UnauthorizedAttemptTracker),
	}

	// Запускаем периодическую проверку эскалированных чатов
	go ar.escalationWatcher()

	// Запускаем очистку старых записей попыток доступа
	go ar.cleanupUnauthorizedAttempts()

	log.Println("[AUTORESPONDER] [SECURITY] Инициализирован с усиленной защитой доступа к заказам")
	log.Println("[AUTORESPONDER] История будет автоматически обрезаться до 20 сообщений (~4000 токенов)")

	return ar
}

// ---------------------------------------------------------------------------
// основная логика
// ---------------------------------------------------------------------------

func (ar *AutoResponder) ProcessMessage(ctx context.Context, chat *models.Chat, msg *models.Message) (*models.Message, error) {
	if !ar.config.Enabled || msg.Sender != "user" {
		return nil, nil
	}
	// чат уже закреплён за оператором
	if chat.AssignedTo != nil && *chat.AssignedTo != uuid.Nil {
		return nil, nil
	}

	chatKey := chat.ID.String()

	// ── проверка запросов о продуктах ПЕРЕД эскалацией ───────────────────────
	// Продукты публичные - не требуют авторизации, обрабатываем всегда
	// Используем оригинальный текст для поиска продуктов, если доступен (до перевода)
	// ─────────────────────────────────────────────────────────
	// ⚠️ УДАЛЕНА СТАРАЯ ЛОГИКА DetectProductQuery + HandleProductQuery
	// Теперь используется Function Calling - LLM сама решает когда
	// вызывать функции get_product_categories / search_products
	// ─────────────────────────────────────────────────────────

	// Проверяем состояние эскалации
	ar.mu.Lock()
	escalation := ar.escalations[chatKey]
	ar.mu.Unlock()

	// Если чат эскалирован, проверяем нужно ли вернуть LLM
	if escalation != nil {
		return ar.handleEscalatedChat(ctx, chat, msg, escalation)
	}

	// ── проверка запросов о заказах ──────────────────────────
	if orderQuery := DetectOrderQuery(msg.Content); orderQuery != nil {
		log.Printf("[AUTORESPONDER] [SECURITY] Обнаружен запрос о заказе в чате %s", chatKey)

		// 🔒 КРИТИЧЕСКАЯ ПРОВЕРКА: проверяем авторизацию ДО обработки
		userID := ExtractUserIDFromChat(ctx, ar.storeClient, chat)
		if userID == 0 {
			// 🔒 RATE LIMITING: Проверяем количество попыток
			ar.mu.Lock()
			tracker := ar.unauthorizedAttempts[chatKey]
			if tracker == nil {
				tracker = &UnauthorizedAttemptTracker{
					Count:        0,
					FirstAttempt: time.Now(),
				}
				ar.unauthorizedAttempts[chatKey] = tracker
			}

			tracker.Count++
			tracker.LastAttempt = time.Now()

			// Проверяем, не заблокирован ли чат
			if tracker.Blocked && tracker.BlockedUntil != nil && time.Now().Before(*tracker.BlockedUntil) {
				ar.mu.Unlock()
				log.Printf("[AUTORESPONDER] [SECURITY] 🚫 Чат %s ЗАБЛОКИРОВАН до %s (попытка %d)",
					chatKey, tracker.BlockedUntil.Format("15:04:05"), tracker.Count)

				blockedUntil := tracker.BlockedUntil.Format("15:04")
				now := time.Now()
				return &models.Message{
					ChatID:    chat.ID,
					Content:   fmt.Sprintf("🚫 Слишком много попыток доступа к заказам без авторизации.\n\nПовторите попытку после %s или обратитесь к оператору.", blockedUntil),
					Sender:    "admin",
					SenderID:  uuid.Nil,
					Timestamp: now,
					Read:      true,
					Type:      "text",
					Metadata: map[string]interface{}{
						"isAutoResponse": true,
						"securityBlock":  true,
						"rateLimited":    true,
					},
				}, nil
			}

			// После 3 попыток - блокируем на 5 минут
			if tracker.Count >= 3 {
				blockUntil := time.Now().Add(5 * time.Minute)
				tracker.Blocked = true
				tracker.BlockedUntil = &blockUntil

				LogSuspiciousActivity(chat.ID, 0, chat.User.Email, fmt.Sprintf("Множественные попытки доступа к заказам без авторизации (%d попыток)", tracker.Count))
				ar.mu.Unlock()

				log.Printf("[AUTORESPONDER] [SECURITY] 🚨 Чат %s ЗАБЛОКИРОВАН на 5 минут (попытки: %d)", chatKey, tracker.Count)

				now := time.Now()
				return &models.Message{
					ChatID:    chat.ID,
					Content:   "🚫 Обнаружено слишком много попыток доступа к заказам без авторизации.\n\nВаш чат временно заблокирован на 5 минут. Пожалуйста, укажите корректный email для идентификации или обратитесь к оператору.",
					Sender:    "admin",
					SenderID:  uuid.Nil,
					Timestamp: now,
					Read:      true,
					Type:      "text",
					Metadata: map[string]interface{}{
						"isAutoResponse": true,
						"securityBlock":  true,
						"rateLimited":    true,
						"blocked":        true,
					},
				}, nil
			}
			ar.mu.Unlock()

			// 🚨 SECURITY EVENT: Неавторизованная попытка доступа
			LogUnauthorizedOrderAccess(chat.ID, orderQuery.OrderID, chat.User.Email)
			log.Printf("[AUTORESPONDER] [SECURITY] 🚨 БЛОКИРОВАН неавторизованный запрос о заказе в чате %s (email: %s, orderID: %s, попытка: %d)",
				chatKey, chat.User.Email, orderQuery.OrderID, tracker.Count)

			// 🔒 ЖЕСТКАЯ БЛОКИРОВКА: Возвращаем отказ напрямую, НЕ давая LLM шанс обработать
			// Это предотвращает prompt injection и социальную инженерию
			now := time.Now()
			blockedMsg := &models.Message{
				ChatID:    chat.ID,
				Content:   fmt.Sprintf("🔒 Для просмотра информации о заказах необходимо авторизоваться.\n\nПожалуйста, укажите ваш email, который вы использовали при регистрации в магазине.\n\n⚠️ Попытка %d/3", tracker.Count),
				Sender:    "admin",
				SenderID:  uuid.Nil,
				Timestamp: now,
				Read:      true,
				Type:      "text",
				Metadata: map[string]interface{}{
					"isAutoResponse":     true,
					"botName":            ar.config.BotName,
					"securityBlock":      true,
					"unauthorizedAccess": true,
					"attemptCount":       tracker.Count,
				},
			}

			log.Printf("[AUTORESPONDER] [SECURITY] ✓ Отправлен автоматический отказ в доступе (попытка %d/3)", tracker.Count)
			return blockedMsg, nil
		}

		// ✅ Пользователь авторизован - очищаем счетчик попыток
		ar.mu.Lock()
		delete(ar.unauthorizedAttempts, chatKey)
		ar.mu.Unlock()

		log.Printf("[AUTORESPONDER] [SECURITY] ✓ Авторизованный запрос о заказе: user_id=%d, chat=%s", userID, chatKey)
		return ar.handleOrderQueryMessage(ctx, chat, msg, orderQuery)
	}

	// ── история ───────────────────────────────────────────────
	ar.mu.Lock()
	hist := ar.history[chatKey]

	// 🔒 ВЫБОР ПРОМПТА: Используем разные промпты для авторизованных и неавторизованных
	if len(hist) == 0 {
		// Проверяем авторизацию для выбора правильного системного промпта
		userID := ExtractUserIDFromChat(ctx, ar.storeClient, chat)
		var selectedPrompt string

		if userID > 0 {
			selectedPrompt = systemPromptAuthorized
			log.Printf("[AUTORESPONDER] [SECURITY] Инициализация истории с АВТОРИЗОВАННЫМ промптом для user_id=%d", userID)
		} else {
			selectedPrompt = systemPromptUnauthorized
			log.Printf("[AUTORESPONDER] [SECURITY] Инициализация истории с НЕАВТОРИЗОВАННЫМ промптом (нет доступа к заказам)")
		}

		hist = []Message{{Role: "system", Content: selectedPrompt}}
	}

	hist = append(hist, Message{Role: "user", Content: msg.Content})

	// Обрезаем историю до разумного размера
	hist = ar.historyMgr.TrimHistory(hist)

	ar.history[chatKey] = hist
	ar.mu.Unlock()

	// имитация «печатает…»
	if ar.config.DelaySeconds > 0 {
		select {
		case <-time.After(time.Duration(ar.config.DelaySeconds) * time.Second):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	genCtx, cancel := context.WithTimeout(ctx, time.Duration(ar.config.IdleTimeMinutes)*time.Minute)
	defer cancel()

	// 🔧 FUNCTION CALLING: Пробуем использовать function calling для product queries
	tools := GetStoreFunctionTools()
	textResp, funcCall, err := ar.client.GenerateResponseWithTools(genCtx, msg.Content, hist, tools)
	if err != nil {
		return nil, fmt.Errorf("GenerateResponseWithTools: %w", err)
	}

	var rawResp string

	// Если LLM хочет вызвать функцию - выполняем её
	if funcCall != nil {
		log.Printf("[FUNCTION_CALLING] LLM вызывает функцию: %s с аргументами: %+v", funcCall.Name, funcCall.Args)

		// Выполняем функцию
		toolCall := ToolCall{
			Name:      funcCall.Name,
			Arguments: funcCall.Args,
		}
		funcResult, err := ExecuteTool(genCtx, ar.storeClient, toolCall)
		if err != nil {
			log.Printf("[FUNCTION_CALLING] Ошибка выполнения функции %s: %v", funcCall.Name, err)
			funcResult = fmt.Sprintf("Error executing function: %v", err)
		}

		log.Printf("[FUNCTION_CALLING] Результат функции %s: %s", funcCall.Name, funcResult)

		// Отправляем результат обратно LLM для финального ответа
		finalResp, err := ar.client.ContinueWithFunctionResult(genCtx, hist, funcCall, funcResult)
		if err != nil {
			return nil, fmt.Errorf("ContinueWithFunctionResult: %w", err)
		}

		rawResp = finalResp
		log.Printf("[FUNCTION_CALLING] Финальный ответ LLM: %s", rawResp)
	} else {
		// Обычный текстовый ответ без function call
		rawResp = textResp
	}

	// ── фильтр самоидентификации и проверка тегов эскалации ──
	clean, escalate := sanitize(rawResp)

	// Дополнительная проверка: ищем теги эскалации в тексте
	if !escalate && strings.Contains(clean, "#эскалация") {
		escalate = true
	}

	if escalate {
		// Проверяем, не эскалирован ли уже этот чат
		ar.mu.Lock()
		existingEscalation := ar.escalations[chatKey]
		ar.mu.Unlock()

		if existingEscalation != nil && existingEscalation.ReturnedAt == nil {
			// Чат уже эскалирован и LLM еще не вернулся
			log.Printf("ProcessMessage: чат %s уже эскалирован, пропускаем повторную эскалацию", chatKey)
			escalate = false // Не эскалируем повторно
			clean = "Ваш запрос уже передан нашему специалисту. Ожидайте ответа."
		} else {
			// Если текст уже содержит сообщение об эскалации, оставляем его
			if !strings.Contains(clean, "#эскалация") {
				clean = "Позвольте подключить нашего старшего специалиста. Одну минутку, пожалуйста. 🙏"
			}

			// Сохраняем состояние эскалации
			ar.mu.Lock()
			ar.escalations[chatKey] = &EscalationState{
				EscalatedAt:   time.Now(),
				AdminNotified: true,
				ReturnedAt:    nil,
			}
			ar.mu.Unlock()
		}
	}

	// ── формируем сообщение ──────────────────────────────────
	now := time.Now()
	botMsg := &models.Message{
		ChatID:    chat.ID,
		Content:   clean,
		Sender:    "admin",
		SenderID:  uuid.Nil,
		Timestamp: now,
		Read:      true,
		Type:      "text",
		Metadata: map[string]interface{}{
			"isAutoResponse": true,
			"botName":        ar.config.BotName,
			"needEscalation": escalate,
		},
	}

	// сохраняем в локальную историю
	ar.mu.Lock()
	histForSave := ar.history[chatKey]
	histForSave = append(histForSave, Message{Role: "assistant", Content: clean})

	// Обрезаем историю после добавления ответа
	histForSave = ar.historyMgr.TrimHistory(histForSave)

	ar.history[chatKey] = histForSave
	ar.mu.Unlock()

	return botMsg, nil
}

// handleEscalatedChat обрабатывает сообщения в эскалированном чате
func (ar *AutoResponder) handleEscalatedChat(ctx context.Context, chat *models.Chat, msg *models.Message, escalation *EscalationState) (*models.Message, error) {
	// Проверяем, прошло ли 5 минут с момента эскалации
	const escalationTimeout = 5 * time.Minute
	if time.Since(escalation.EscalatedAt) > escalationTimeout && escalation.ReturnedAt == nil {
		// Проверяем, отвечал ли адиин после эскалации
		adminAnswered := ar.checkAdminResponse(chat, escalation.EscalatedAt)

		if !adminAnswered {
			// Админ не ответил, возвращаем LLM с извинением
			now := time.Now()
			ar.mu.Lock()
			escalation.ReturnedAt = &now
			ar.mu.Unlock()

			apologeticMsg := &models.Message{
				ChatID:    chat.ID,
				Content:   "Прошу прощения за ожидание! Наш специалист временно недоступен. Я постараюсь помочь вам сам. Пожалуйста, повторите свой вопрос. 🙏",
				Sender:    "admin",
				SenderID:  uuid.Nil,
				Timestamp: now,
				Read:      true,
				Type:      "text",
				Metadata: map[string]interface{}{
					"isAutoResponse": true,
					"isApology":      true,
					"escalationEnd":  true,
				},
			}

			// Продолжаем нормальную обработку
			return apologeticMsg, nil
		}
	}

	// Если админ уже ответил или время еще не истекло, LLM не отвечает
	return nil, nil
}

// checkAdminResponse проверяет, отвечал ли админ после указанного времени
func (ar *AutoResponder) checkAdminResponse(chat *models.Chat, after time.Time) bool {
	// Загружаем последние сообщения из базы данных
	chatWithMessages, _, err := database.GetChatByID(chat.ID, 1, 20) // Последние 20 сообщений
	if err != nil {
		log.Printf("checkAdminResponse: ошибка загрузки сообщений: %v", err)
		return false
	}

	// Проверяем сообщения
	for i := len(chatWithMessages.Messages) - 1; i >= 0; i-- {
		msg := chatWithMessages.Messages[i]
		if msg.Timestamp.After(after) && msg.Sender == "admin" {
			// Проверяем, что это не автоответ
			if metadata, ok := msg.Metadata["isAutoResponse"].(bool); !ok || !metadata {
				return true
			}
		}
	}

	return false
}

// ClearEscalation очищает состояние эскалации для чата
func (ar *AutoResponder) ClearEscalation(chatID string) {
	ar.mu.Lock()
	delete(ar.escalations, chatID)
	ar.mu.Unlock()
}

// SetApologyCallback устанавливает callback для отправки сообщений извинения
func (ar *AutoResponder) SetApologyCallback(callback func(chatID uuid.UUID, message *models.Message)) {
	ar.onApologyMessage = callback
}

// escalationWatcher периодически проверяет эскалированные чаты и возвращает LLM при необходимости
func (ar *AutoResponder) escalationWatcher() {
	ticker := time.NewTicker(1 * time.Minute) // Проверяем каждую минуту
	defer ticker.Stop()

	for range ticker.C {
		ar.checkEscalations()
	}
}

// checkEscalations проверяет все эскалированные чаты и отправляет извинения если нужно
func (ar *AutoResponder) checkEscalations() {
	ar.mu.RLock()
	escalationsToCheck := make(map[string]*EscalationState)
	for chatID, escalation := range ar.escalations {
		escalationsToCheck[chatID] = escalation
	}
	ar.mu.RUnlock()

	const escalationTimeout = 5 * time.Minute

	for chatID, escalation := range escalationsToCheck {
		if escalation.ReturnedAt != nil {
			continue // Уже вернулся
		}

		if time.Since(escalation.EscalatedAt) > escalationTimeout {
			// Загружаем чат из базы
			chatUUID, err := uuid.Parse(chatID)
			if err != nil {
				log.Printf("escalationWatcher: ошибка парсинга chatID %s: %v", chatID, err)
				continue
			}

			lightChat, err := queries.GetChatLightweight(database.DB, chatUUID)
			if err != nil {
				log.Printf("escalationWatcher: ошибка загрузки чата %s: %v", chatID, err)
				continue
			}

			// Проверяем отвечал ли админ
			adminAnswered := ar.checkAdminResponse(lightChat, escalation.EscalatedAt)
			if !adminAnswered {
				// Отправляем извинение
				ar.sendApologyMessage(lightChat)
			}
		}
	}
}

// sendApologyMessage отправляет сообщение-извинение от LLM
func (ar *AutoResponder) sendApologyMessage(chat *models.Chat) {
	now := time.Now()
	chatKey := chat.ID.String()

	// Обновляем состояние эскалации
	ar.mu.Lock()
	if escalation := ar.escalations[chatKey]; escalation != nil {
		escalation.ReturnedAt = &now
	}
	ar.mu.Unlock()

	// Создаем сообщение извинения
	apologeticMsg := &models.Message{
		ChatID:    chat.ID,
		Content:   "Прошу прощения за ожидание! Наш специалист временно недоступен. Я постараюсь помочь вам сам. Пожалуйста, повторите свой вопрос. 🙏",
		Sender:    "admin",
		SenderID:  uuid.Nil,
		Timestamp: now,
		Read:      true,
		Type:      "text",
		Metadata: map[string]interface{}{
			"isAutoResponse": true,
			"isApology":      true,
			"escalationEnd":  true,
		},
	}

	// Сохраняем в базу данных
	saved, err := database.AddMessage(
		chat.ID,
		apologeticMsg.Content,
		apologeticMsg.Sender,
		apologeticMsg.SenderID,
		apologeticMsg.Type,
		apologeticMsg.Metadata,
	)
	if err != nil {
		log.Printf("sendApologyMessage: ошибка сохранения сообщения: %v", err)
		return
	}

	log.Printf("escalationWatcher: отправлено извинение в чат %s", chatKey)

	// Здесь нужно отправить WebSocket уведомление
	// Но у нас нет доступа к WebSocketHub отсюда
	// Поэтому добавим callback функцию
	if ar.onApologyMessage != nil {
		ar.onApologyMessage(chat.ID, saved)
	}
}

// handleOrderQueryMessage обрабатывает запросы о заказах
func (ar *AutoResponder) handleOrderQueryMessage(ctx context.Context, chat *models.Chat, msg *models.Message, query *OrderQuery) (*models.Message, error) {
	// Извлекаем user_id из магазина
	userID := ExtractUserIDFromChat(ctx, ar.storeClient, chat)

	if userID == 0 {
		log.Printf("[AUTORESPONDER] Не удалось определить user_id магазина для чата %s (user email: %s)",
			chat.ID, chat.User.Email)

		// Если пользователь не найден, LLM все равно может попробовать помочь
		// Но запросы о заказах будут ограничены
	} else {
		log.Printf("[AUTORESPONDER] Определен user_id магазина: %d для чата %s", userID, chat.ID)

		// Сохраняем найденный user_id в метаданных чата для будущих запросов
		if chat.Metadata == nil {
			chat.Metadata = make(map[string]interface{})
		}
		chat.Metadata["store_user_id"] = userID
	}

	// Обрабатываем запрос через HandleOrderQuery
	orderInfo, err := HandleOrderQuery(ctx, ar.storeClient, query, userID)
	if err != nil {
		log.Printf("[AUTORESPONDER] Ошибка обработки запроса о заказе: %v", err)
		// Если не удалось получить информацию о заказе, передаем обработку LLM
		return nil, nil
	}

	// Формируем ответное сообщение
	now := time.Now()
	botMsg := &models.Message{
		ChatID:    chat.ID,
		Content:   orderInfo,
		Sender:    "admin",
		SenderID:  uuid.Nil,
		Timestamp: now,
		Read:      true,
		Type:      "text",
		Metadata: map[string]interface{}{
			"isAutoResponse": true,
			"botName":        ar.config.BotName,
			"orderQuery":     true,
		},
	}

	log.Printf("[AUTORESPONDER] Отправка информации о заказе в чат %s", chat.ID)

	return botMsg, nil
}

// ---------------------------------------------------------------------------
// работа с БД
// ---------------------------------------------------------------------------

func (ar *AutoResponder) SaveChatHistory(ctx context.Context, chatID string, tx *sql.Tx) error {
	ar.mu.RLock()
	hist := ar.history[chatID]
	ar.mu.RUnlock()
	if len(hist) == 0 {
		return nil
	}
	raw, err := json.Marshal(hist)
	if err != nil {
		return fmt.Errorf("SaveChatHistory: marshal: %w", err)
	}

	query := `
		UPDATE chats
		SET metadata = jsonb_set(coalesce(metadata, '{}'::jsonb), '{llmHistory}', $1)
		WHERE id = $2
	`
	if tx != nil {
		_, err = tx.ExecContext(ctx, query, raw, chatID)
	} else {
		_, err = database.DB.ExecContext(ctx, query, raw, chatID)
	}
	return err
}

func (ar *AutoResponder) LoadChatHistory(ctx context.Context, chatID string) error {
	var raw []byte
	query := `SELECT metadata->'llmHistory' FROM chats WHERE id = $1`
	if err := database.DB.QueryRowContext(ctx, query, chatID).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return fmt.Errorf("LoadChatHistory: scan: %w", err)
	}
	if len(raw) == 0 {
		return nil
	}
	var hist []Message
	if err := json.Unmarshal(raw, &hist); err != nil {
		return fmt.Errorf("LoadChatHistory: unmarshal: %w", err)
	}
	ar.mu.Lock()
	ar.history[chatID] = hist
	ar.mu.Unlock()
	return nil
}

func (ar *AutoResponder) ClearChatHistory(chatID string) {
	ar.mu.Lock()
	delete(ar.history, chatID)
	ar.mu.Unlock()
}

// cleanupUnauthorizedAttempts периодически очищает старые записи попыток доступа
func (ar *AutoResponder) cleanupUnauthorizedAttempts() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		ar.mu.Lock()
		now := time.Now()
		for chatKey, tracker := range ar.unauthorizedAttempts {
			// Удаляем записи старше 1 часа
			if now.Sub(tracker.LastAttempt) > 1*time.Hour {
				delete(ar.unauthorizedAttempts, chatKey)
				log.Printf("[AUTORESPONDER] [SECURITY] Очищена запись о попытках доступа для чата %s", chatKey)
			}
			// Разблокируем чаты, у которых истекло время блокировки
			if tracker.Blocked && tracker.BlockedUntil != nil && now.After(*tracker.BlockedUntil) {
				tracker.Blocked = false
				tracker.BlockedUntil = nil
				tracker.Count = 0
				log.Printf("[AUTORESPONDER] [SECURITY] Чат %s разблокирован", chatKey)
			}
		}
		ar.mu.Unlock()
	}
}

// ResetUnauthorizedAttempts сбрасывает счетчик попыток для чата (вызывается при успешной авторизации)
func (ar *AutoResponder) ResetUnauthorizedAttempts(chatID string) {
	ar.mu.Lock()
	delete(ar.unauthorizedAttempts, chatID)
	ar.mu.Unlock()
	log.Printf("[AUTORESPONDER] [SECURITY] Сброшен счетчик попыток для чата %s", chatID)
}
