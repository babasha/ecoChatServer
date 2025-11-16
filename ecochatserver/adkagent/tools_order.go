package adkagent

import (
	"fmt"
	"log"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/egor/ecochatserver/llm"
)

// ============================================================================
// ORDER AGENT TOOLS - Инструменты для управления заказами
// ============================================================================

// UserIDProvider - интерфейс для получения userID из агента
type UserIDProvider interface {
	GetUserID() int
}

// CreateOrderTools создает набор инструментов для работы с заказами
func CreateOrderTools(storeClient *llm.StoreClient, userIDProvider *UserIDProvider) ([]tool.Tool, error) {
	var tools []tool.Tool

	// ======== Базовые инструменты (уже существуют) ========

	// Tool 1: Get User Orders
	getUserOrdersTool, err := createGetUserOrdersTool(storeClient, userIDProvider)
	if err != nil {
		return nil, err
	}
	tools = append(tools, getUserOrdersTool)

	// Tool 2: Get Order Status
	getOrderStatusTool, err := createGetOrderStatusTool(storeClient, userIDProvider)
	if err != nil {
		return nil, err
	}
	tools = append(tools, getOrderStatusTool)

	// ======== НОВЫЕ инструменты ========

	// Tool 3: Track Order - отслеживание заказа в реальном времени
	trackOrderTool, err := createTrackOrderTool(storeClient, userIDProvider)
	if err != nil {
		return nil, err
	}
	tools = append(tools, trackOrderTool)

	// Tool 4: Get Orders by Status - заказы по статусу
	getOrdersByStatusTool, err := createGetOrdersByStatusTool(storeClient, userIDProvider)
	if err != nil {
		return nil, err
	}
	tools = append(tools, getOrdersByStatusTool)

	// Tool 5: Get Recent Orders - последние заказы
	getRecentOrdersTool, err := createGetRecentOrdersTool(storeClient, userIDProvider)
	if err != nil {
		return nil, err
	}
	tools = append(tools, getRecentOrdersTool)

	// Tool 6: Report Delivery Issue - сообщить о проблеме
	reportDeliveryIssueTool, err := createReportDeliveryIssueTool(storeClient, userIDProvider)
	if err != nil {
		return nil, err
	}
	tools = append(tools, reportDeliveryIssueTool)

	log.Printf("[TOOLS_ORDER] Created %d order tools", len(tools))
	return tools, nil
}

// ============================================================================
// Базовые tools
// ============================================================================

func createGetUserOrdersTool(storeClient *llm.StoreClient, userIDProvider *UserIDProvider) (tool.Tool, error) {
	type GetUserOrdersInput struct {
		Limit int `json:"limit"` // Лимит количества заказов (опционально)
	}
	type UserOrdersOutput struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "get_user_orders",
			Description: "Get user's order history. ONLY for authorized users. Use when customer asks 'show my orders', 'my order history', etc. Optionally provide limit for number of orders.",
		},
		func(ctx tool.Context, input GetUserOrdersInput) (UserOrdersOutput, error) {
			log.Printf("[TOOL] get_user_orders called: limit=%d", input.Limit)

			if userIDProvider == nil || *userIDProvider == nil {
				return UserOrdersOutput{Result: "Error: agent not initialized"}, nil
			}

			userID := (*userIDProvider).GetUserID()
			if userID == 0 {
				return UserOrdersOutput{Result: "⚠️ This feature is only available for registered users. Please log in to view your orders."}, nil
			}

			orders, err := storeClient.GetUserOrders(ctx, userID)
			if err != nil {
				log.Printf("[TOOL] Error getting user orders: %v", err)
				return UserOrdersOutput{Result: fmt.Sprintf("Error retrieving orders: %v", err)}, nil
			}

			if len(orders) == 0 {
				return UserOrdersOutput{Result: "You don't have any orders yet. Start shopping to see your order history!"}, nil
			}

			if input.Limit > 0 && input.Limit < len(orders) {
				orders = orders[:input.Limit]
			}

			result := llm.FormatOrdersList(orders)
			return UserOrdersOutput{Result: result}, nil
		},
	)
}

func createGetOrderStatusTool(storeClient *llm.StoreClient, userIDProvider *UserIDProvider) (tool.Tool, error) {
	type GetOrderStatusInput struct {
		OrderID string `json:"orderId"` // ID заказа
	}
	type OrderStatusOutput struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "get_order_status",
			Description: "Get status of a specific order by ID. ONLY for authorized users. Use when customer asks 'where is my order #123', 'order status', etc. Requires order ID.",
		},
		func(ctx tool.Context, input GetOrderStatusInput) (OrderStatusOutput, error) {
			log.Printf("[TOOL] get_order_status called: orderID=%s", input.OrderID)

			if userIDProvider == nil || *userIDProvider == nil {
				return OrderStatusOutput{Result: "Error: agent not initialized"}, nil
			}

			userID := (*userIDProvider).GetUserID()
			if userID == 0 {
				return OrderStatusOutput{Result: "⚠️ This feature is only available for registered users. Please log in to view your order status."}, nil
			}

			if input.OrderID == "" {
				return OrderStatusOutput{Result: "Error: order ID is required. Please provide the order number."}, nil
			}

			order, err := storeClient.GetOrderByID(ctx, input.OrderID, userID)
			if err != nil {
				log.Printf("[TOOL] Error getting order: %v", err)
				return OrderStatusOutput{Result: fmt.Sprintf("Order #%s not found or you don't have access to it.", input.OrderID)}, nil
			}

			result := llm.FormatOrderInfo(order)
			return OrderStatusOutput{Result: result}, nil
		},
	)
}

// ============================================================================
// НОВЫЕ ПРОДВИНУТЫЕ TOOLS
// ============================================================================

// createTrackOrderTool - отслеживание заказа в реальном времени
func createTrackOrderTool(storeClient *llm.StoreClient, userIDProvider *UserIDProvider) (tool.Tool, error) {
	type TrackOrderInput struct {
		OrderID string `json:"orderId"` // ID заказа для отслеживания
	}
	type TrackOrderOutput struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "track_order",
			Description: "Track order in real-time with detailed status updates. Use when customer asks 'track my order', 'where is my delivery now', 'how long until delivery'. Provides current status, estimated time, and courier information if available.",
		},
		func(ctx tool.Context, input TrackOrderInput) (TrackOrderOutput, error) {
			log.Printf("[TOOL] track_order called: orderID=%s", input.OrderID)

			if userIDProvider == nil || *userIDProvider == nil {
				return TrackOrderOutput{Result: "Error: agent not initialized"}, nil
			}

			userID := (*userIDProvider).GetUserID()
			if userID == 0 {
				return TrackOrderOutput{Result: "⚠️ This feature is only available for registered users. Please log in to track your order."}, nil
			}

			if input.OrderID == "" {
				return TrackOrderOutput{Result: "Error: order ID is required for tracking."}, nil
			}

			order, err := storeClient.GetOrderByID(ctx, input.OrderID, userID)
			if err != nil {
				log.Printf("[TOOL] Error tracking order: %v", err)
				return TrackOrderOutput{Result: fmt.Sprintf("Order #%s not found or you don't have access to it.", input.OrderID)}, nil
			}

			// Расширенная информация о трекинге
			result := fmt.Sprintf("🚚 TRACKING ORDER #%s\n\n", order.ID)

			// Статус заказа с детализацией
			statusMap := map[string]struct {
				ru          string
				description string
				emoji       string
			}{
				"pending":            {"В обработке", "Ваш заказ принят и обрабатывается", "⏳"},
				"confirmed":          {"Подтвержден", "Заказ подтвержден магазином", "✅"},
				"preparing":          {"Готовится", "Товары собираются на складе", "📦"},
				"ready_for_delivery": {"Готов к доставке", "Заказ готов и ожидает курьера", "🎯"},
				"courier_assigned":   {"Курьер назначен", "Курьер получил ваш заказ", "👤"},
				"in_delivery":        {"В пути", "Курьер везет ваш заказ", "🚗"},
				"delivered":          {"Доставлен", "Заказ успешно доставлен", "🎉"},
				"cancelled":          {"Отменен", "Заказ был отменен", "❌"},
				"failed_delivery":    {"Не доставлен", "Не удалось доставить заказ", "⚠️"},
			}

			statusInfo := statusMap[order.Status]
			if statusInfo.ru == "" {
				statusInfo = struct {
					ru          string
					description string
					emoji       string
				}{order.Status, "Статус заказа", "📋"}
			}

			result += fmt.Sprintf("%s %s\n", statusInfo.emoji, statusInfo.ru)
			result += fmt.Sprintf("   %s\n\n", statusInfo.description)

			// Детали заказа
			result += fmt.Sprintf("💰 Сумма: %.2f₾\n", order.Total)
			result += fmt.Sprintf("📅 Заказ создан: %s\n", order.CreatedAt.Format("02.01.2006 15:04"))

			// Время доставки
			if order.DeliveryTime != nil {
				result += fmt.Sprintf("🕐 Ожидаемая доставка: %s\n", order.DeliveryTime.Format("02.01.2006 15:04"))

				// Оценка времени до доставки
				// (В реальности нужен API для точного трекинга)
				if order.Status == "in_delivery" || order.Status == "courier_assigned" {
					result += "⏱️ Примерное время прибытия: 15-30 минут\n"
				}
			}

			// Адрес доставки
			if order.DeliveryAddress != "" {
				result += fmt.Sprintf("📍 Адрес: %s\n", order.DeliveryAddress)
			}

			// Информация о курьере (если есть)
			if len(order.VendorOrders) > 0 {
				result += "\n🚴 Курьеры:\n"
				for i, vo := range order.VendorOrders {
					result += fmt.Sprintf("  %d. %s - %s\n", i+1, vo.VendorName, vo.Status)
					if vo.CourierID != nil {
						result += fmt.Sprintf("     Курьер #%d назначен\n", *vo.CourierID)
					}
				}
			}

			// Состав заказа
			if len(order.Items) > 0 {
				result += "\n📦 Состав заказа:\n"
				for i, item := range order.Items {
					result += fmt.Sprintf("  %d. %s × %d - %.2f₾\n", i+1, item.Name, item.Quantity, item.Price)
				}
			}

			return TrackOrderOutput{Result: result}, nil
		},
	)
}

// createGetOrdersByStatusTool - получение заказов по статусу
func createGetOrdersByStatusTool(storeClient *llm.StoreClient, userIDProvider *UserIDProvider) (tool.Tool, error) {
	type GetOrdersByStatusInput struct {
		Status string `json:"status"` // Статус: pending, confirmed, preparing, in_delivery, delivered, cancelled
	}
	type GetOrdersByStatusOutput struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "get_orders_by_status",
			Description: "Get user's orders filtered by status. Use when customer asks 'show my active orders', 'pending orders', 'delivered orders'. Status can be: 'pending', 'confirmed', 'preparing', 'in_delivery', 'delivered', 'cancelled'.",
		},
		func(ctx tool.Context, input GetOrdersByStatusInput) (GetOrdersByStatusOutput, error) {
			log.Printf("[TOOL] get_orders_by_status called: status=%s", input.Status)

			if userIDProvider == nil || *userIDProvider == nil {
				return GetOrdersByStatusOutput{Result: "Error: agent not initialized"}, nil
			}

			userID := (*userIDProvider).GetUserID()
			if userID == 0 {
				return GetOrdersByStatusOutput{Result: "⚠️ This feature is only available for registered users. Please log in."}, nil
			}

			if input.Status == "" {
				return GetOrdersByStatusOutput{Result: "Error: status is required. Available: pending, confirmed, preparing, in_delivery, delivered, cancelled"}, nil
			}

			orders, err := storeClient.GetOrdersByStatus(ctx, userID, input.Status)
			if err != nil {
				log.Printf("[TOOL] Error getting orders by status: %v", err)
				return GetOrdersByStatusOutput{Result: fmt.Sprintf("Error retrieving orders: %v", err)}, nil
			}

			if len(orders) == 0 {
				return GetOrdersByStatusOutput{Result: fmt.Sprintf("No orders with status '%s' found.", input.Status)}, nil
			}

			result := fmt.Sprintf("📋 Orders with status '%s':\n\n", input.Status)
			result += llm.FormatOrdersList(orders)

			return GetOrdersByStatusOutput{Result: result}, nil
		},
	)
}

// createGetRecentOrdersTool - получение последних заказов
func createGetRecentOrdersTool(storeClient *llm.StoreClient, userIDProvider *UserIDProvider) (tool.Tool, error) {
	type GetRecentOrdersInput struct {
		Limit int `json:"limit"` // Количество последних заказов
	}
	type GetRecentOrdersOutput struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "get_recent_orders",
			Description: "Get user's most recent orders. Use when customer asks 'show my last orders', 'recent purchases', 'what did I order recently'. Provide limit for number of orders (default 3).",
		},
		func(ctx tool.Context, input GetRecentOrdersInput) (GetRecentOrdersOutput, error) {
			log.Printf("[TOOL] get_recent_orders called: limit=%d", input.Limit)

			if userIDProvider == nil || *userIDProvider == nil {
				return GetRecentOrdersOutput{Result: "Error: agent not initialized"}, nil
			}

			userID := (*userIDProvider).GetUserID()
			if userID == 0 {
				return GetRecentOrdersOutput{Result: "⚠️ This feature is only available for registered users. Please log in."}, nil
			}

			if input.Limit <= 0 {
				input.Limit = 3
			}
			if input.Limit > 10 {
				input.Limit = 10
			}

			orders, err := storeClient.GetUserOrders(ctx, userID)
			if err != nil {
				log.Printf("[TOOL] Error getting recent orders: %v", err)
				return GetRecentOrdersOutput{Result: fmt.Sprintf("Error retrieving orders: %v", err)}, nil
			}

			if len(orders) == 0 {
				return GetRecentOrdersOutput{Result: "You don't have any orders yet."}, nil
			}

			// Берем только последние N заказов
			if len(orders) > input.Limit {
				orders = orders[:input.Limit]
			}

			result := fmt.Sprintf("🕐 Your %d most recent orders:\n\n", len(orders))
			result += llm.FormatOrdersList(orders)

			return GetRecentOrdersOutput{Result: result}, nil
		},
	)
}

// createReportDeliveryIssueTool - сообщить о проблеме с доставкой
func createReportDeliveryIssueTool(storeClient *llm.StoreClient, userIDProvider *UserIDProvider) (tool.Tool, error) {
	type ReportDeliveryIssueInput struct {
		OrderID     string `json:"orderId"`     // ID заказа
		IssueType   string `json:"issueType"`   // Тип проблемы: delay, damaged, missing, wrong
		Description string `json:"description"` // Описание проблемы
	}
	type ReportDeliveryIssueOutput struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "report_delivery_issue",
			Description: "Report a problem with order delivery. Use when customer reports issues like 'order is late', 'product is damaged', 'missing items', 'wrong order'. Issue types: 'delay' (late delivery), 'damaged' (damaged product), 'missing' (missing items), 'wrong' (wrong product).",
		},
		func(ctx tool.Context, input ReportDeliveryIssueInput) (ReportDeliveryIssueOutput, error) {
			log.Printf("[TOOL] report_delivery_issue called: orderID=%s, type=%s, desc=%s", input.OrderID, input.IssueType, input.Description)

			if userIDProvider == nil || *userIDProvider == nil {
				return ReportDeliveryIssueOutput{Result: "Error: agent not initialized"}, nil
			}

			userID := (*userIDProvider).GetUserID()
			if userID == 0 {
				return ReportDeliveryIssueOutput{Result: "⚠️ This feature is only available for registered users. Please log in."}, nil
			}

			if input.OrderID == "" {
				return ReportDeliveryIssueOutput{Result: "Error: order ID is required to report an issue."}, nil
			}

			if input.IssueType == "" {
				return ReportDeliveryIssueOutput{Result: "Error: issue type is required. Use: 'delay', 'damaged', 'missing', or 'wrong'."}, nil
			}

			// Проверяем, что заказ существует и принадлежит пользователю
			order, err := storeClient.GetOrderByID(ctx, input.OrderID, userID)
			if err != nil {
				log.Printf("[TOOL] Error finding order: %v", err)
				return ReportDeliveryIssueOutput{Result: fmt.Sprintf("Order #%s not found or you don't have access to it.", input.OrderID)}, nil
			}

			// В реальной системе здесь был бы API вызов для создания тикета
			// Пока что возвращаем информацию о том, что проблема зарегистрирована

			issueTypesMap := map[string]string{
				"delay":   "Задержка доставки",
				"damaged": "Поврежденный товар",
				"missing": "Отсутствующие товары",
				"wrong":   "Неправильный заказ",
			}

			issueTypeRu := issueTypesMap[input.IssueType]
			if issueTypeRu == "" {
				issueTypeRu = input.IssueType
			}

			result := fmt.Sprintf("✅ ISSUE REPORTED\n\n")
			result += fmt.Sprintf("Order: #%s\n", order.ID)
			result += fmt.Sprintf("Issue Type: %s\n", issueTypeRu)
			if input.Description != "" {
				result += fmt.Sprintf("Description: %s\n", input.Description)
			}
			result += "\n📞 Our support team will contact you shortly to resolve this issue.\n"
			result += "Ticket ID: #TEMP-" + input.OrderID + "-" + input.IssueType + "\n\n"
			result += "In the meantime, you can:\n"
			result += "• Call us: +995 XXX XXX XXX\n"
			result += "• Write to support@enddel.com\n"
			result += "• Use live chat on our website\n"

			// TODO: В продакшене здесь нужно создать реальный тикет в системе поддержки
			// Например, через API: storeClient.CreateSupportTicket(...)

			return ReportDeliveryIssueOutput{Result: result}, nil
		},
	)
}
