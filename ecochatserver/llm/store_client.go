package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"
)

// StoreClient — клиент для работы с API магазина
type StoreClient struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// Order представляет заказ из магазина
type Order struct {
	ID              string                 `json:"id"`
	UserID          int                    `json:"user_id"`
	FirstName       string                 `json:"first_name"`
	LastName        string                 `json:"last_name"`
	Phone           string                 `json:"phone"`
	Address         string                 `json:"address"`
	Total           float64                `json:"total"`
	Status          string                 `json:"status"`
	CreatedAt       time.Time              `json:"created_at"`
	DeliveryTime    *time.Time             `json:"delivery_time"`
	DeliveryAddress string                 `json:"delivery_address"`
	PaymentMethod   string                 `json:"payment_method"`
	Items           []OrderItem            `json:"items"`
	VendorOrders    []VendorOrder          `json:"vendor_orders,omitempty"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
}

// OrderItem представляет товар в заказе
type OrderItem struct {
	ProductID   int     `json:"product_id"`
	Name        string  `json:"name"`
	Quantity    int     `json:"quantity"`
	Price       float64 `json:"price"`
	VendorID    int     `json:"vendor_id,omitempty"`
	VendorName  string  `json:"vendor_name,omitempty"`
	ImageURL    string  `json:"image_url,omitempty"`
	Description string  `json:"description,omitempty"`
}

// VendorOrder представляет под-заказ вендора
type VendorOrder struct {
	VendorOrderID int         `json:"vendor_order_id"`
	VendorID      int         `json:"vendor_id"`
	VendorName    string      `json:"vendor_name"`
	Subtotal      float64     `json:"subtotal"`
	Status        string      `json:"status"`
	CourierID     *int        `json:"courier_id"`
	Items         []OrderItem `json:"items"`
}

// OrderDetailsResponse представляет детальную информацию о заказе
type OrderDetailsResponse struct {
	Order        Order  `json:"order"`
	TrackingInfo string `json:"tracking_info,omitempty"`
}

// NewStoreClient создает новый клиент для работы с API магазина
// Поддерживает hot-swap через переменные окружения и БД
func NewStoreClient() *StoreClient {
	// Приоритет: 1) ENV, 2) БД, 3) дефолт
	// Но для БД используем database.GetSetting если доступно
	baseURL := os.Getenv("STORE_API_URL")
	if baseURL == "" {
		// Пытаемся загрузить из БД (если database пакет доступен)
		// baseURL = database.GetSetting("STORE_API_URL", "https://enddelclientbekendnode-enddelbackend.up.railway.app")
		baseURL = "https://enddelclientbekendnode-enddelbackend.up.railway.app" // Продакшен по умолчанию
	}

	apiKey := os.Getenv("STORE_API_KEY")
	if apiKey == "" {
		// apiKey = database.GetSetting("STORE_API_KEY", "")
		apiKey = "" // Можно добавить дефолтный API ключ если нужен
	}

	timeout := 15 * time.Second
	if t := os.Getenv("STORE_API_TIMEOUT"); t != "" {
		if d, err := time.ParseDuration(t); err == nil {
			timeout = d
		}
	}

	log.Printf("[STORE_CLIENT] Инициализирован: baseURL=%s, timeout=%v", baseURL, timeout)

	return &StoreClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		client: &http.Client{
			Timeout: timeout,
			Transport: &customTransport{
				Base: http.DefaultTransport,
			},
		},
	}
}

// customTransport добавляет реалистичный User-Agent для обхода bot-блокировки
type customTransport struct {
	Base http.RoundTripper
}

func (t *customTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Устанавливаем реалистичный User-Agent вместо "Go-http-client/2.0"
	// Используем стандартный браузерный UA для избежания блокировки bot traffic
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	return t.Base.RoundTrip(req)
}

// GetBaseURL возвращает базовый URL API магазина
func (sc *StoreClient) GetBaseURL() string {
	return sc.baseURL
}

// GetUserOrders получает все заказы пользователя по user_id
func (sc *StoreClient) GetUserOrders(ctx context.Context, userID int) ([]Order, error) {
	endpoint := fmt.Sprintf("%s/api/messenger/orders/user/%d", sc.baseURL, userID)
	return sc.fetchOrders(ctx, endpoint)
}

// GetOrderByID получает информацию о конкретном заказе по его ID
// ВАЖНО: требует userID для проверки владельца заказа
func (sc *StoreClient) GetOrderByID(ctx context.Context, orderID string, userID int) (*Order, error) {
	if userID == 0 {
		return nil, fmt.Errorf("user_id is required for security")
	}

	endpoint := fmt.Sprintf("%s/api/messenger/orders/%s?user_id=%d", sc.baseURL, orderID, userID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if sc.apiKey != "" {
		req.Header.Set("X-API-Key", sc.apiKey)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := sc.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("store API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("store API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	var order Order
	if err := json.NewDecoder(resp.Body).Decode(&order); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &order, nil
}

// GetOrdersByPhone получает заказы по номеру телефона
func (sc *StoreClient) GetOrdersByPhone(ctx context.Context, phone string) ([]Order, error) {
	endpoint := fmt.Sprintf("%s/api/messenger/orders/search?phone=%s", sc.baseURL, phone)
	return sc.fetchOrders(ctx, endpoint)
}

// GetOrdersByStatus получает заказы пользователя по статусу
func (sc *StoreClient) GetOrdersByStatus(ctx context.Context, userID int, status string) ([]Order, error) {
	endpoint := fmt.Sprintf("%s/api/messenger/orders/user/%d?status=%s", sc.baseURL, userID, status)
	return sc.fetchOrders(ctx, endpoint)
}

// fetchOrders — вспомогательная функция для получения списка заказов
func (sc *StoreClient) fetchOrders(ctx context.Context, endpoint string) ([]Order, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if sc.apiKey != "" {
		req.Header.Set("X-API-Key", sc.apiKey)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := sc.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("store API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("store API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	var orders []Order
	if err := json.NewDecoder(resp.Body).Decode(&orders); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return orders, nil
}

// FormatOrderInfo форматирует информацию о заказе для отображения пользователю
func FormatOrderInfo(order *Order) string {
	if order == nil {
		return "Заказ не найден."
	}

	statusMap := map[string]string{
		"pending":            "В обработке",
		"confirmed":          "Подтвержден",
		"preparing":          "Готовится",
		"ready_for_delivery": "Готов к доставке",
		"courier_assigned":   "Курьер назначен",
		"in_delivery":        "В пути",
		"delivered":          "Доставлен",
		"cancelled":          "Отменен",
		"failed_delivery":    "Не удалось доставить",
	}

	statusRu := statusMap[order.Status]
	if statusRu == "" {
		statusRu = order.Status
	}

	result := fmt.Sprintf("📦 Заказ #%s\n", order.ID)
	result += fmt.Sprintf("📊 Статус: %s\n", statusRu)
	result += fmt.Sprintf("💰 Сумма: %.2f руб.\n", order.Total)
	result += fmt.Sprintf("📅 Дата: %s\n", order.CreatedAt.Format("02.01.2006 15:04"))

	if order.DeliveryTime != nil {
		result += fmt.Sprintf("🕐 Время доставки: %s\n", order.DeliveryTime.Format("02.01.2006 15:04"))
	}

	if order.DeliveryAddress != "" {
		result += fmt.Sprintf("📍 Адрес: %s\n", order.DeliveryAddress)
	}

	if len(order.Items) > 0 {
		result += "\n🛒 Товары:\n"
		for i, item := range order.Items {
			result += fmt.Sprintf("%d. %s x%d — %.2f руб.\n", i+1, item.Name, item.Quantity, item.Price)
		}
	}

	return result
}

// FindUserByEmail ищет пользователя в магазине по email
func (sc *StoreClient) FindUserByEmail(ctx context.Context, email string) (int, error) {
	endpoint := fmt.Sprintf("%s/api/messenger/users/find?email=%s", sc.baseURL, email)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}

	if sc.apiKey != "" {
		req.Header.Set("X-API-Key", sc.apiKey)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := sc.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("store API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return 0, nil // Пользователь не найден
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("store API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	var result struct {
		UserID int `json:"user_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}

	return result.UserID, nil
}

// FindUserByPhone ищет пользователя в магазине по телефону
func (sc *StoreClient) FindUserByPhone(ctx context.Context, phone string) (int, error) {
	endpoint := fmt.Sprintf("%s/api/messenger/users/find?phone=%s", sc.baseURL, phone)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}

	if sc.apiKey != "" {
		req.Header.Set("X-API-Key", sc.apiKey)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := sc.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("store API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return 0, nil // Пользователь не найден
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("store API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	var result struct {
		UserID int `json:"user_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}

	return result.UserID, nil
}

// FormatOrdersList форматирует список заказов для отображения
func FormatOrdersList(orders []Order) string {
	if len(orders) == 0 {
		return "У вас пока нет заказов."
	}

	statusMap := map[string]string{
		"pending":            "В обработке",
		"confirmed":          "Подтвержден",
		"preparing":          "Готовится",
		"ready_for_delivery": "Готов к доставке",
		"courier_assigned":   "Курьер назначен",
		"in_delivery":        "В пути",
		"delivered":          "Доставлен",
		"cancelled":          "Отменен",
		"failed_delivery":    "Не удалось доставить",
	}

	result := fmt.Sprintf("📦 Найдено заказов: %d\n\n", len(orders))

	for i, order := range orders {
		if i >= 5 { // Ограничиваем вывод первыми 5 заказами
			result += fmt.Sprintf("\n... и еще %d заказов", len(orders)-5)
			break
		}

		statusRu := statusMap[order.Status]
		if statusRu == "" {
			statusRu = order.Status
		}

		result += fmt.Sprintf("%d. Заказ #%s\n", i+1, order.ID)
		result += fmt.Sprintf("   Статус: %s\n", statusRu)
		result += fmt.Sprintf("   Сумма: %.2f руб.\n", order.Total)
		result += fmt.Sprintf("   Дата: %s\n\n", order.CreatedAt.Format("02.01.2006"))
	}

	return result
}

// Product представляет товар из магазина
type Product struct {
	ID            int    `json:"id"`
	NameRu        string `json:"name_ru"`
	NameEn        string `json:"name_en"`
	NamePt        string `json:"name_pt"`
	Description   string `json:"description"`
	Price         string `json:"price"` // API возвращает строку вместо числа
	InStock       bool   `json:"in_stock"`
	StockQuantity int    `json:"stock_quantity"` // Количество на складе
	CategoryID    *int   `json:"category_id"`
	ImageURL      string `json:"image_url"`
	Slug          string `json:"slug"`
}

// Category представляет категорию товаров
type Category struct {
	ID       int    `json:"id"`
	NameRu   string `json:"name_ru"`
	NameEn   string `json:"name_en"`
	NamePt   string `json:"name_pt"`
	NameEs   string `json:"name_es"`
	ParentID *int   `json:"parent_id"`
}

// GetAllProducts получает список всех доступных продуктов
// searchQuery - опциональный параметр для поиска товаров по названию/описанию
func (sc *StoreClient) GetAllProducts(ctx context.Context, searchQuery string) ([]Product, error) {
	endpoint := fmt.Sprintf("%s/products", sc.baseURL)

	// Добавляем параметр поиска если указан (с URL encoding)
	if searchQuery != "" {
		endpoint = fmt.Sprintf("%s?search=%s", endpoint, url.QueryEscape(searchQuery))
	}

	log.Printf("[STORE_CLIENT] Запрос к API магазина: %s", endpoint)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", sc.apiKey)
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; EcoChatBot/1.0)")

	resp, err := sc.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("store API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("store API returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Products []Product `json:"products"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return result.Products, nil
}

// GetAllCategories получает список всех категорий товаров из магазина
func (sc *StoreClient) GetAllCategories(ctx context.Context) ([]Category, error) {
	endpoint := fmt.Sprintf("%s/categories", sc.baseURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", sc.apiKey)

	resp, err := sc.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("store API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("store API returned %d: %s", resp.StatusCode, string(body))
	}

	var categories []Category
	if err := json.NewDecoder(resp.Body).Decode(&categories); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return categories, nil
}

// GetProductBySlug получает информацию о конкретном продукте по slug
func (sc *StoreClient) GetProductBySlug(ctx context.Context, slug string) (*Product, error) {
	endpoint := fmt.Sprintf("%s/products/by-slug/%s", sc.baseURL, slug)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := sc.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("store API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("product not found")
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("store API returned %d: %s", resp.StatusCode, string(body))
	}

	var product Product
	if err := json.NewDecoder(resp.Body).Decode(&product); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &product, nil
}
