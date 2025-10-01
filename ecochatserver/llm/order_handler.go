package llm

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/egor/ecochatserver/models"
)

// OrderQueryType представляет тип запроса о заказе
type OrderQueryType int

const (
	OrderQueryUnknown OrderQueryType = iota
	OrderQueryByID                   // Запрос по конкретному ID заказа
	OrderQueryList                   // Запрос списка заказов
	OrderQueryStatus                 // Запрос статуса заказа
	OrderQueryTrack                  // Отслеживание заказа
)

// OrderQuery представляет распознанный запрос о заказе
type OrderQuery struct {
	Type    OrderQueryType
	OrderID string
	UserID  int
	Phone   string
}

// DetectOrderQuery пытается распознать запрос о заказе в сообщении пользователя
func DetectOrderQuery(message string) *OrderQuery {
	msgLower := strings.ToLower(message)

	// Паттерны для распознавания запросов о заказах
	patterns := []struct {
		pattern string
		qtype   OrderQueryType
	}{
		// Запросы по конкретному ID
		{`заказ\s*#?(\d+)`, OrderQueryByID},
		{`order\s*#?(\d+)`, OrderQueryByID},
		{`номер\s*заказа\s*#?(\d+)`, OrderQueryByID},

		// Запросы списка заказов
		{`мои\s*заказы`, OrderQueryList},
		{`покажи?\s*(мои)?\s*заказы`, OrderQueryList},
		{`список\s*заказов`, OrderQueryList},
		{`где\s*мои\s*заказы`, OrderQueryList},
		{`my\s*orders`, OrderQueryList},

		// Запросы статуса
		{`статус\s*заказа`, OrderQueryStatus},
		{`где\s*(мой)?\s*заказ`, OrderQueryStatus},
		{`когда\s*доставят`, OrderQueryTrack},
		{`где\s*курьер`, OrderQueryTrack},
		{`order\s*status`, OrderQueryStatus},
		{`track\s*order`, OrderQueryTrack},
	}

	for _, p := range patterns {
		re := regexp.MustCompile(p.pattern)
		if matches := re.FindStringSubmatch(msgLower); matches != nil {
			query := &OrderQuery{Type: p.qtype}

			// Если нашли ID заказа в тексте
			if len(matches) > 1 && matches[1] != "" {
				query.OrderID = matches[1]
			}

			return query
		}
	}

	// Дополнительная проверка: если сообщение содержит только цифры, это может быть ID заказа
	if matched, _ := regexp.MatchString(`^\d+$`, strings.TrimSpace(message)); matched {
		return &OrderQuery{
			Type:    OrderQueryByID,
			OrderID: strings.TrimSpace(message),
		}
	}

	return nil
}

// HandleOrderQuery обрабатывает запрос о заказе и возвращает ответ
func HandleOrderQuery(ctx context.Context, storeClient *StoreClient, query *OrderQuery, userID int) (string, error) {
	if storeClient == nil {
		return "", fmt.Errorf("store client not initialized")
	}

	// 🔒 КРИТИЧЕСКАЯ ПРОВЕРКА: userID обязателен для всех операций с заказами
	if userID == 0 {
		log.Printf("[ORDER_HANDLER] [SECURITY] Попытка доступа к заказам без авторизации")
		return "Для просмотра заказов необходимо авторизоваться. Пожалуйста, укажите ваш email или номер телефона.", nil
	}

	log.Printf("[ORDER_HANDLER] [SECURITY] Обработка запроса: type=%d, orderID=%s, userID=%d", query.Type, query.OrderID, userID)

	switch query.Type {
	case OrderQueryByID:
		return handleOrderByID(ctx, storeClient, query.OrderID, userID)

	case OrderQueryList:
		return handleOrdersList(ctx, storeClient, userID)

	case OrderQueryStatus, OrderQueryTrack:
		// Если есть конкретный ID - показываем его
		if query.OrderID != "" {
			return handleOrderByID(ctx, storeClient, query.OrderID, userID)
		}
		// Иначе показываем последние заказы
		return handleRecentOrders(ctx, storeClient, userID)

	default:
		return "", fmt.Errorf("unknown query type")
	}
}

// handleOrderByID получает информацию о конкретном заказе
// 🔒 БЕЗОПАСНОСТЬ: Проверяет принадлежность заказа пользователю
func handleOrderByID(ctx context.Context, storeClient *StoreClient, orderID string, userID int) (string, error) {
	log.Printf("[ORDER_HANDLER] [SECURITY] Запрос заказа %s пользователем %d", orderID, userID)

	order, err := storeClient.GetOrderByID(ctx, orderID, userID)
	if err != nil {
		if strings.Contains(err.Error(), "user_id is required") {
			log.Printf("[ORDER_HANDLER] [SECURITY] Отклонен запрос заказа %s: не авторизован", orderID)
			return "Для просмотра информации о заказе необходимо авторизоваться.", nil
		}
		// 🚨 Попытка доступа к чужому заказу или несуществующему заказу
		log.Printf("[ORDER_HANDLER] [SECURITY] ❌ Заказ %s не найден для пользователя %d: %v", orderID, userID, err)
		return "Извините, я не смог найти этот заказ. Возможно, он был создан под другим аккаунтом или номер заказа указан неверно.", nil
	}

	// ✅ Успешный доступ к заказу
	log.Printf("[ORDER_HANDLER] [SECURITY] ✓ Доступ к заказу %s предоставлен пользователю %d", orderID, userID)
	return FormatOrderInfo(order), nil
}

// handleOrdersList получает список всех заказов пользователя
func handleOrdersList(ctx context.Context, storeClient *StoreClient, userID int) (string, error) {
	if userID == 0 {
		return "Для просмотра ваших заказов необходимо авторизоваться.", nil
	}

	orders, err := storeClient.GetUserOrders(ctx, userID)
	if err != nil {
		log.Printf("[ORDER_HANDLER] Ошибка получения заказов пользователя %d: %v", userID, err)
		return "Извините, произошла ошибка при получении списка заказов. Попробуйте позже.", nil
	}

	return FormatOrdersList(orders), nil
}

// handleRecentOrders получает последние активные заказы
func handleRecentOrders(ctx context.Context, storeClient *StoreClient, userID int) (string, error) {
	if userID == 0 {
		return "Для отслеживания заказов необходимо авторизоваться.", nil
	}

	// Получаем заказы в статусе "в процессе"
	activeStatuses := []string{
		"pending",
		"confirmed",
		"preparing",
		"ready_for_delivery",
		"courier_assigned",
		"in_delivery",
	}

	allOrders, err := storeClient.GetUserOrders(ctx, userID)
	if err != nil {
		log.Printf("[ORDER_HANDLER] Ошибка получения заказов: %v", err)
		return "Извините, не удалось получить информацию о заказах.", nil
	}

	// Фильтруем активные заказы
	var activeOrders []Order
	for _, order := range allOrders {
		for _, status := range activeStatuses {
			if order.Status == status {
				activeOrders = append(activeOrders, order)
				break
			}
		}
	}

	if len(activeOrders) == 0 {
		return "У вас нет активных заказов в данный момент.", nil
	}

	return FormatOrdersList(activeOrders), nil
}

// ExtractUserIDFromContext пытается извлечь user_id из контекста или метаданных чата
// Поддерживает несколько источников: metadata, store_user_id, автоматический поиск по email
func ExtractUserIDFromContext(ctx context.Context, storeClient *StoreClient, chat interface{}) int {
	// Преобразуем chat в нужный тип
	type ChatInfo struct {
		Metadata map[string]interface{}
		User     struct {
			Email string
			Name  string
		}
	}

	// Попробуем привести к нужному типу через type assertion
	var chatInfo ChatInfo
	if chatMap, ok := chat.(map[string]interface{}); ok {
		if metadata, ok := chatMap["metadata"].(map[string]interface{}); ok {
			chatInfo.Metadata = metadata
		}
		if user, ok := chatMap["user"].(map[string]interface{}); ok {
			if email, ok := user["email"].(string); ok {
				chatInfo.User.Email = email
			}
		}
	}

	// 1. Проверяем, есть ли сохраненный store_user_id в метаданных
	if chatInfo.Metadata != nil {
		if userID, ok := chatInfo.Metadata["store_user_id"].(int); ok && userID > 0 {
			return userID
		}
		if userID, ok := chatInfo.Metadata["store_user_id"].(float64); ok && userID > 0 {
			return int(userID)
		}
	}

	// 2. Если есть email, пытаемся найти пользователя в магазине
	if chatInfo.User.Email != "" && storeClient != nil {
		userID, err := storeClient.FindUserByEmail(ctx, chatInfo.User.Email)
		if err != nil {
			log.Printf("[ORDER_HANDLER] Ошибка поиска пользователя по email: %v", err)
		} else if userID > 0 {
			log.Printf("[ORDER_HANDLER] Найден пользователь магазина по email: user_id=%d", userID)
			return userID
		}
	}

	return 0
}

// ExtractUserIDFromChat - упрощенная версия для использования с моделью Chat
func ExtractUserIDFromChat(ctx context.Context, storeClient *StoreClient, chat *models.Chat) int {
	// 1. Проверяем метаданные чата
	if chat.Metadata != nil {
		if userID, ok := chat.Metadata["store_user_id"].(int); ok && userID > 0 {
			return userID
		}
		if userID, ok := chat.Metadata["store_user_id"].(float64); ok && userID > 0 {
			return int(userID)
		}
	}

	// 2. Пытаемся найти по email
	if chat.User.Email != "" && storeClient != nil {
		userID, err := storeClient.FindUserByEmail(ctx, chat.User.Email)
		if err != nil {
			log.Printf("[ORDER_HANDLER] Ошибка поиска пользователя по email: %v", err)
		} else if userID > 0 {
			log.Printf("[ORDER_HANDLER] Найден пользователь магазина по email: user_id=%d", userID)
			return userID
		}
	}

	return 0
}
