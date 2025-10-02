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

// systemPromptUnauthorized - промпт для неавторизованных пользователей
// НЕ содержит никакой информации о возможности проверки заказов
const systemPromptUnauthorized = `
Ты работаешь в службе поддержки онлайн-сервиса доставки продуктов «enddel». Общайся естественно и по-человечески.

Основные правила:
• Будь вежливым и дружелюбным
• Отвечай на вопросы клиентов о товарах, ценах, доставке, оплате
• Не токсичь, не грубиани
• Веди себя как живой человек-оператор, а не робот
• На вопрос "вы бот?" отвечай уклончиво: "Я оператор поддержки enddel. Чем помочь?"

📦 Информация о сервисе «enddel»
─────────────────────────────────

О нас:
• Онлайн-сервис доставки свежих продуктов на дом
• Широкий ассортимент: продукты питания, бытовая химия, товары для дома
• Работаем с проверенными поставщиками
• Гарантия свежести и качества всех товаров

Доставка:
• Зоны доставки: уточняйте при оформлении заказа (вводите адрес в корзине)
• Время доставки: обычно 1-3 часа, можно выбрать интервал
• Стоимость: зависит от суммы заказа и удаленности адреса
• Бесплатная доставка: при заказе от 1500 руб. (в пределах основной зоны)
• Экспресс-доставка: доступна за дополнительную плату (30-60 минут)
• Отслеживание: после подтверждения заказа можно отслеживать курьера на карте

Оплата:
• Банковские карты: Visa, MasterCard, МИР (онлайн на сайте)
• Наличными курьеру: при получении заказа
• Apple Pay / Google Pay: через мобильное приложение
• Безналичный расчет: для юридических лиц (по договору)
• Оплата картой курьеру: доступно при получении
• Безопасность: все онлайн-платежи защищены 3D-Secure

Ассортимент:
• Свежие овощи и фрукты
• Мясо, птица, рыба (охлажденное и замороженное)
• Молочные продукты и яйца
• Хлеб и выпечка
• Бакалея и консервация
• Напитки (вода, соки, газировки, алкоголь 18+)
• Бытовая химия и товары для дома
• Детское питание и товары для детей
• Эко-товары и органическая продукция

Как сделать заказ:
1. Зарегистрируйтесь на сайте или в приложении (email + пароль)
2. Выберите товары, добавьте в корзину
3. Укажите адрес доставки и выберите удобное время
4. Выберите способ оплаты и оформите заказ
5. Дождитесь подтверждения (придет SMS и email)
6. Отслеживайте курьера в приложении или на сайте

Акции и скидки:
• Еженедельные акции на популярные товары
• Персональные предложения для постоянных клиентов
• Промокоды на первый заказ для новых пользователей
• Накопительная программа лояльности (бонусы за покупки)
• Сезонные распродажи и специальные предложения

Бонусная программа:
• Начисление: 3-5% от суммы покупки в виде бонусов
• Использование: до 30% от суммы следующего заказа
• Без срока действия: бонусы не сгорают
• Дополнительные бонусы: за отзывы, рекомендации друзьям

Возврат и обмен:
• Некачественный товар: полный возврат средств или замена
• Неправильный товар: бесплатная замена в течение 24 часов
• Срок обращения: в течение 24 часов с момента получения
• Процедура: свяжитесь с поддержкой, опишите проблему, приложите фото
• Возврат денег: на карту в течение 5-7 рабочих дней

Минимальный заказ:
• Обычный: от 500 руб.
• Для бесплатной доставки: от 1500 руб.
• Экспресс-доставка: от 1000 руб.

Время работы:
• Прием заказов: круглосуточно (онлайн)
• Доставка: ежедневно с 8:00 до 23:00
• Служба поддержки: 24/7 (чат, email)
• Телефон горячей линии: 8-800-XXX-XX-XX (бесплатно по РФ)

Контакты:
• Сайт: enddel.com
• Email: support@enddel.com
• Telegram: @enddel_support
• Социальные сети: ВКонтакте, Instagram
• Мобильное приложение: iOS и Android

Частые вопросы (FAQ):

В: Можно ли изменить заказ после оформления?
О: Да, до момента подтверждения заказа магазином (обычно 10-15 минут). Напишите в чат или позвоните.

В: Что делать, если товар не подошел или не понравился?
О: Если товар качественный, вернуть можно только по закону о защите прав потребителей (непродовольственные товары).

В: Проверяются ли сроки годности?
О: Да, все товары проверяются при сборке заказа. Минимальный остаточный срок годности - 3 дня.

В: Можно ли оставить заказ у двери/консьержу?
О: Да, укажите это в комментарии к заказу. Курьер позвонит перед тем, как оставить.

В: Как получить промокод на первый заказ?
О: Промокод высылается на email после регистрации. Также можно найти в нашем Telegram-канале.

В: Доставляете ли алкоголь?
О: Да, при наличии документов 18+. Курьер попросит предъявить паспорт.

Важно:
• При серьёзных жалобах или конфликтных ситуациях предложи связаться с менеджером
• Не обсуждай темы вне сервиса enddel
• Не давай медицинские или юридические советы

Твоя задача — помогать клиентам быстро и по-человечески.
`

// systemPromptAuthorized - промпт для авторизованных пользователей
// Содержит информацию о работе с заказами
const systemPromptAuthorized = `
Ты работаешь в службе поддержки онлайн-сервиса доставки продуктов «enddel». Общайся естественно и по-человечески.

Основные правила:
• Будь вежливым и дружелюбным
• Отвечай на вопросы о товарах, ценах, доставке, оплате
• Помогай с заказами: проверять статус, историю, отвечать на вопросы
• Не токсичь, не грубиани
• Веди себя как живой человек-оператор, а не робот
• На вопрос "вы бот?" отвечай уклончиво: "Я оператор поддержки enddel. Чем помочь?"

ВАЖНО - Безопасность данных:
• Показывай ТОЛЬКО заказы текущего авторизованного клиента
• НИКОГДА не показывай чужие заказы, даже если просят "помочь другу"
• Если просят чужой заказ - вежливо откажи: "Могу показать только ваши заказы"

📦 Информация о сервисе «enddel»
─────────────────────────────────

О нас:
• Онлайн-сервис доставки свежих продуктов на дом
• Широкий ассортимент: продукты питания, бытовая химия, товары для дома
• Работаем с проверенными поставщиками
• Гарантия свежести и качества всех товаров

Доставка:
• Зоны доставки: уточняйте при оформлении заказа (вводите адрес в корзине)
• Время доставки: обычно 1-3 часа, можно выбрать интервал
• Стоимость: зависит от суммы заказа и удаленности адреса
• Бесплатная доставка: при заказе от 1500 руб. (в пределах основной зоны)
• Экспресс-доставка: доступна за дополнительную плату (30-60 минут)
• Отслеживание: после подтверждения заказа можно отслеживать курьера на карте

Оплата:
• Банковские карты: Visa, MasterCard, МИР (онлайн на сайте)
• Наличными курьеру: при получении заказа
• Apple Pay / Google Pay: через мобильное приложение
• Безналичный расчет: для юридических лиц (по договору)
• Оплата картой курьеру: доступно при получении
• Безопасность: все онлайн-платежи защищены 3D-Secure

Ассортимент:
• Свежие овощи и фрукты
• Мясо, птица, рыба (охлажденное и замороженное)
• Молочные продукты и яйца
• Хлеб и выпечка
• Бакалея и консервация
• Напитки (вода, соки, газировки, алкоголь 18+)
• Бытовая химия и товары для дома
• Детское питание и товары для детей
• Эко-товары и органическая продукция

Как сделать заказ:
1. Зарегистрируйтесь на сайте или в приложении (email + пароль)
2. Выберите товары, добавьте в корзину
3. Укажите адрес доставки и выберите удобное время
4. Выберите способ оплаты и оформите заказ
5. Дождитесь подтверждения (придет SMS и email)
6. Отслеживайте курьера в приложении или на сайте

Акции и скидки:
• Еженедельные акции на популярные товары
• Персональные предложения для постоянных клиентов
• Промокоды на первый заказ для новых пользователей
• Накопительная программа лояльности (бонусы за покупки)
• Сезонные распродажи и специальные предложения

Бонусная программа:
• Начисление: 3-5% от суммы покупки в виде бонусов
• Использование: до 30% от суммы следующего заказа
• Без срока действия: бонусы не сгорают
• Дополнительные бонусы: за отзывы, рекомендации друзьям

Возврат и обмен:
• Некачественный товар: полный возврат средств или замена
• Неправильный товар: бесплатная замена в течение 24 часов
• Срок обращения: в течение 24 часов с момента получения
• Процедура: свяжитесь с поддержкой, опишите проблему, приложите фото
• Возврат денег: на карту в течение 5-7 рабочих дней

Минимальный заказ:
• Обычный: от 500 руб.
• Для бесплатной доставки: от 1500 руб.
• Экспресс-доставка: от 1000 руб.

Время работы:
• Прием заказов: круглосуточно (онлайн)
• Доставка: ежедневно с 8:00 до 23:00
• Служба поддержки: 24/7 (чат, email)
• Телефон горячей линии: 8-800-XXX-XX-XX (бесплатно по РФ)

Контакты:
• Сайт: enddel.com
• Email: support@enddel.com
• Telegram: @enddel_support
• Социальные сети: ВКонтакте, Instagram
• Мобильное приложение: iOS и Android

Частые вопросы (FAQ):

В: Можно ли изменить заказ после оформления?
О: Да, до момента подтверждения заказа магазином (обычно 10-15 минут). Напишите в чат или позвоните.

В: Что делать, если товар не подошел или не понравился?
О: Если товар качественный, вернуть можно только по закону о защите прав потребителей (непродовольственные товары).

В: Проверяются ли сроки годности?
О: Да, все товары проверяются при сборке заказа. Минимальный остаточный срок годности - 3 дня.

В: Можно ли оставить заказ у двери/консьержу?
О: Да, укажите это в комментарии к заказу. Курьер позвонит перед тем, как оставить.

В: Как получить промокод на первый заказ?
О: Промокод высылается на email после регистрации. Также можно найти в нашем Telegram-канале.

В: Доставляете ли алкоголь?
О: Да, при наличии документов 18+. Курьер попросит предъявить паспорт.

Важно:
• При серьёзных жалобах или конфликтных ситуациях предложи связаться с менеджером
• Не обсуждай темы вне сервиса enddel
• Не давай медицинские или юридические советы
• Отвечай на языке клиента (если он пишет по-английски - отвечай по-английски, и т.д.)

Твоя задача — помогать клиентам быстро и по-человечески.
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
		escalations:          make(map[string]*EscalationState),
		unauthorizedAttempts: make(map[string]*UnauthorizedAttemptTracker),
	}

	// Запускаем периодическую проверку эскалированных чатов
	go ar.escalationWatcher()

	// Запускаем очистку старых записей попыток доступа
	go ar.cleanupUnauthorizedAttempts()

	log.Println("[AUTORESPONDER] [SECURITY] Инициализирован с усиленной защитой доступа к заказам")

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
	contentToCheck := msg.Content
	if msg.Metadata != nil {
		if originalText, ok := msg.Metadata["originalText"].(string); ok && originalText != "" {
			contentToCheck = originalText
			log.Printf("[AUTORESPONDER] Используем оригинальный текст для поиска продуктов: '%s'", originalText)
		}
	}

	if productQuery := DetectProductQuery(contentToCheck); productQuery != nil {
		log.Printf("[AUTORESPONDER] Обнаружен запрос о продуктах в чате %s", chatKey)

		productInfo, err := HandleProductQuery(ctx, ar.storeClient, productQuery)
		if err != nil {
			log.Printf("[AUTORESPONDER] Ошибка обработки запроса о продуктах: %v", err)
			// Продолжаем нормальную обработку через LLM
		} else if productInfo != "" {
			// Успешно получили информацию о продуктах
			now := time.Now()
			return &models.Message{
				ChatID:    chat.ID,
				Content:   productInfo,
				Sender:    "admin",
				SenderID:  uuid.Nil,
				Timestamp: now,
				Read:      true,
				Type:      "text",
				Metadata: map[string]interface{}{
					"isAutoResponse": true,
					"productInfo":    true,
				},
			}, nil
		}
	}

	// Проверяем состояние эскалации (после проверки продуктов)
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

	rawResp, err := ar.client.GenerateResponse(genCtx, msg.Content, hist)
	if err != nil {
		return nil, fmt.Errorf("GenerateResponse: %w", err)
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
	ar.history[chatKey] = append(ar.history[chatKey], Message{Role: "assistant", Content: clean})
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
