package adkagent

import (
	"fmt"
	"log"
	"strings"
	"time"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/egor/ecochatserver/llm"
)

// ============================================================================
// PRODUCT AGENT TOOLS - Инструменты для продуктового консультанта
// ============================================================================

// ============================================================================
// SESSION STATE OPTIMIZATION - Константы для кэша
// ============================================================================

const (
	// Кэш категорий (живет всю сессию)
	SessionKeyCategoriesCache = "app:categories_cache"
	SessionKeyCategoriesTTL   = "app:categories_ttl"

	// Контекст пользователя
	SessionKeyUserInterest     = "user:interested_in"
	SessionKeyUserLastCategory = "user:last_category"
)

// CreateProductTools создает набор инструментов для работы с продуктами
func CreateProductTools(storeClient *llm.StoreClient) ([]tool.Tool, error) {
	var tools []tool.Tool

	// ======== Базовые инструменты (уже существуют) ========

	// Tool 1: Get Products
	getProductsTool, err := createGetProductsTool(storeClient)
	if err != nil {
		return nil, err
	}
	tools = append(tools, getProductsTool)

	// Tool 2: Search Product
	searchProductTool, err := createSearchProductTool(storeClient)
	if err != nil {
		return nil, err
	}
	tools = append(tools, searchProductTool)

	// Tool 3: Get Categories
	getCategoriesTool, err := createGetCategoriesTool(storeClient)
	if err != nil {
		return nil, err
	}
	tools = append(tools, getCategoriesTool)

	// Tool 4: Check Product Availability
	checkAvailabilityTool, err := createCheckAvailabilityTool(storeClient)
	if err != nil {
		return nil, err
	}
	tools = append(tools, checkAvailabilityTool)

	// ======== НОВЫЕ инструменты ========

	// Tool 5: Compare Products - сравнение товаров
	compareProductsTool, err := createCompareProductsTool(storeClient)
	if err != nil {
		return nil, err
	}
	tools = append(tools, compareProductsTool)

	// Tool 6: Recommend Products - умные рекомендации
	recommendProductsTool, err := createRecommendProductsTool(storeClient)
	if err != nil {
		return nil, err
	}
	tools = append(tools, recommendProductsTool)

	// Tool 7: Find Alternatives - поиск аналогов
	findAlternativesTool, err := createFindAlternativesTool(storeClient)
	if err != nil {
		return nil, err
	}
	tools = append(tools, findAlternativesTool)

	// Tool 8: Get Products by Category - товары по категории
	getProductsByCategoryTool, err := createGetProductsByCategoryTool(storeClient)
	if err != nil {
		return nil, err
	}
	tools = append(tools, getProductsByCategoryTool)

	log.Printf("[TOOLS_PRODUCT] Created %d product tools", len(tools))
	return tools, nil
}

// ============================================================================
// Базовые tools
// ============================================================================

func createGetProductsTool(storeClient *llm.StoreClient) (tool.Tool, error) {
	type GetProductsInput struct {
		Query    string `json:"query,omitempty"`    // Поисковый запрос (опционально) - для Gemini/Gemma
		Category string `json:"category,omitempty"` // Категория или поиск (опционально) - для других моделей типа gpt-oss
	}
	type ProductsOutput struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "get_products",
			Description: "Search and get actual products from database. Use 'query' or 'category' parameter to filter (e.g., query='wine', category='напитки'). Returns real product names, prices, and availability. THIS IS THE PRIMARY TOOL FOR PRODUCT SEARCHES.",
		},
		func(ctx tool.Context, input GetProductsInput) (ProductsOutput, error) {
			// Поддерживаем оба параметра для совместимости с разными LLM
			searchQuery := input.Query
			if searchQuery == "" && input.Category != "" {
				searchQuery = input.Category
			}

			// Если явно указано "all", получаем все товары
			if searchQuery == "all" {
				searchQuery = ""
			}

			log.Printf("[TOOL] get_products called: query=%s, category=%s, effective_search=%s",
				input.Query, input.Category, searchQuery)

			products, err := storeClient.GetAllProducts(ctx, searchQuery)
			if err != nil {
				log.Printf("[TOOL] get_products error: %v", err)
				return ProductsOutput{Result: fmt.Sprintf("Error: %v", err)}, nil
			}

			if len(products) == 0 {
				log.Printf("[TOOL] get_products: found 0 products for search '%s'", searchQuery)
				result := fmt.Sprintf("No products found for '%s'. The database does not have products matching this search.", searchQuery)
				return ProductsOutput{Result: result}, nil
			}

			log.Printf("[TOOL] get_products: found %d products for search '%s'", len(products), searchQuery)

			if len(products) > 15 {
				products = products[:15]
			}

			result := llm.FormatProductsList(products)
			return ProductsOutput{Result: result}, nil
		},
	)
}

func createSearchProductTool(storeClient *llm.StoreClient) (tool.Tool, error) {
	type SearchProductInput struct {
		Query string `json:"query"` // Параметр для поиска
	}
	type SearchProductOutput struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "search_product",
			Description: "Search product by name.",
		},
		func(ctx tool.Context, input SearchProductInput) (SearchProductOutput, error) {
			log.Printf("[TOOL] search_product called: query=%s", input.Query)

			if input.Query == "" {
				return SearchProductOutput{Result: "Error: search query is required"}, nil
			}

			products, err := storeClient.GetAllProducts(ctx, input.Query)
			if err != nil {
				return SearchProductOutput{Result: fmt.Sprintf("Error: %v", err)}, nil
			}

			if len(products) == 0 {
				return SearchProductOutput{Result: fmt.Sprintf("Product '%s' not found.", input.Query)}, nil
			}

			if len(products) == 1 {
				result := llm.FormatProductDetails(products[0])
				return SearchProductOutput{Result: result}, nil
			}

			if len(products) > 10 {
				products = products[:10]
			}
			result := llm.FormatProductsList(products)
			return SearchProductOutput{Result: result}, nil
		},
	)
}

// createGetCategoriesTool - ОПТИМИЗИРОВАННАЯ версия с Session State кэшированием
func createGetCategoriesTool(storeClient *llm.StoreClient) (tool.Tool, error) {
	type GetCategoriesInput struct {
		// No input needed
	}
	type CategoriesOutput struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "get_categories",
			Description: "Get list of available category names only. WARNING: This does NOT return products - you must call get_products or search_product afterwards to get actual products.",
		},
		func(ctx tool.Context, input GetCategoriesInput) (CategoriesOutput, error) {
			log.Printf("[TOOL] get_categories called")

			// Get session state directly from context
			state := ctx.State()

			// ===== ШАГ 1: Проверяем SESSION CACHE =====
			if cached, err := state.Get(SessionKeyCategoriesCache); err == nil {
				// Проверяем TTL
				if ttl, _ := state.Get(SessionKeyCategoriesTTL); ttl != nil {
					if time.Now().Before(ttl.(time.Time)) {
						log.Printf("[SESSION] ✅ Categories from session cache! (saved API call)")
						return CategoriesOutput{Result: cached.(string)}, nil
					}
					log.Printf("[SESSION] ⏰ Cache expired, fetching fresh data")
				}
			}

			log.Printf("[SESSION] ❌ Cache miss, fetching from API...")

			// ===== ШАГ 2: Запрашиваем из API =====
			categories, err := storeClient.GetAllCategories(ctx)
			if err != nil {
				return CategoriesOutput{Result: fmt.Sprintf("Error: %v", err)}, nil
			}

			if len(categories) == 0 {
				return CategoriesOutput{Result: "No categories found."}, nil
			}

			// Форматируем результат с ID для использования в get_products_by_category
			result := fmt.Sprintf("Store has %d categories:\n\n", len(categories))
			for i, cat := range categories {
				if i >= 20 {
					result += fmt.Sprintf("\n... and %d more categories", len(categories)-20)
					break
				}
				// Показываем ID и название (с родительской категорией если есть)
				parentInfo := ""
				if cat.ParentID != nil {
					parentInfo = fmt.Sprintf(" (subcategory of ID %d)", *cat.ParentID)
				}
				result += fmt.Sprintf("• ID %d: %s%s\n", cat.ID, cat.NameRu, parentInfo)
			}

			// ВАЖНО: Добавляем явную инструкцию для модели
			result += "\n⚠️ NOTE: To get products from a category, use get_products_by_category(category_id=X) where X is the category ID from above."

			// ===== ШАГ 3: Сохраняем в SESSION =====
			state.Set(SessionKeyCategoriesCache, result)
			state.Set(SessionKeyCategoriesTTL, time.Now().Add(10*time.Minute)) // TTL 10 минут

			log.Printf("[SESSION] 💾 Cached categories for 10 minutes")

			return CategoriesOutput{Result: result}, nil
		},
	)
}

func createCheckAvailabilityTool(storeClient *llm.StoreClient) (tool.Tool, error) {
	type CheckProductAvailabilityInput struct {
		ProductSlug string `json:"slug"` // Product slug for availability check
	}
	type ProductAvailabilityOutput struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "check_product_availability",
			Description: "Check product stock by slug.",
		},
		func(ctx tool.Context, input CheckProductAvailabilityInput) (ProductAvailabilityOutput, error) {
			log.Printf("[TOOL] check_product_availability called: slug=%s", input.ProductSlug)

			if input.ProductSlug == "" {
				return ProductAvailabilityOutput{Result: "Error: product slug is required"}, nil
			}

			product, err := storeClient.GetProductBySlug(ctx, input.ProductSlug)
			if err != nil {
				log.Printf("[TOOL] Error getting product: %v", err)
				return ProductAvailabilityOutput{Result: fmt.Sprintf("Product not found or error checking availability: %v", err)}, nil
			}

			result := fmt.Sprintf("📦 %s\n\n", product.NameRu)

			if product.InStock {
				result += "✅ IN STOCK\n"
				if product.StockQuantity > 0 {
					result += fmt.Sprintf("Available quantity: %d\n", product.StockQuantity)
				}
			} else {
				result += "❌ OUT OF STOCK\n"
			}

			result += fmt.Sprintf("\n💰 Price: %s₾", product.Price)

			return ProductAvailabilityOutput{Result: result}, nil
		},
	)
}

// ============================================================================
// НОВЫЕ ПРОДВИНУТЫЕ TOOLS
// ============================================================================

// createCompareProductsTool - сравнение нескольких товаров
func createCompareProductsTool(storeClient *llm.StoreClient) (tool.Tool, error) {
	type CompareProductsInput struct {
		Products []string `json:"products"` // Product names to compare
	}
	type CompareProductsOutput struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "compare_products",
			Description: "Compare multiple products.",
		},
		func(ctx tool.Context, input CompareProductsInput) (CompareProductsOutput, error) {
			log.Printf("[TOOL] compare_products called: products=%v", input.Products)

			if len(input.Products) < 2 {
				return CompareProductsOutput{Result: "Need at least 2 products to compare."}, nil
			}

			if len(input.Products) > 4 {
				return CompareProductsOutput{Result: "Can compare maximum 4 products at once."}, nil
			}

			// Собираем информацию о каждом товаре
			var products []llm.Product
			for _, name := range input.Products {
				prods, err := storeClient.GetAllProducts(ctx, name)
				if err != nil || len(prods) == 0 {
					log.Printf("[TOOL] Product '%s' not found", name)
					continue
				}
				products = append(products, prods[0]) // Берем первый найденный
			}

			if len(products) < 2 {
				return CompareProductsOutput{Result: "Not enough products found for comparison. Please check product names."}, nil
			}

			// Форматируем сравнительную таблицу
			result := "📊 COMPARISON TABLE\n\n"
			result += fmt.Sprintf("Comparing %d products:\n\n", len(products))

			for i, p := range products {
				result += fmt.Sprintf("━━━ Product %d: %s ━━━\n", i+1, p.NameRu)
				result += fmt.Sprintf("💰 Price: %s₾\n", p.Price)
				if p.InStock {
					result += fmt.Sprintf("📦 Stock: ✅ Available (%d pcs)\n", p.StockQuantity)
				} else {
					result += "📦 Stock: ❌ Out of stock\n"
				}
				if p.Description != "" {
					desc := p.Description
					if len(desc) > 100 {
						desc = desc[:100] + "..."
					}
					result += fmt.Sprintf("📝 Info: %s\n", desc)
				}
				result += "\n"
			}

			return CompareProductsOutput{Result: result}, nil
		},
	)
}

// createRecommendProductsTool - умные рекомендации
func createRecommendProductsTool(storeClient *llm.StoreClient) (tool.Tool, error) {
	type RecommendProductsInput struct {
		Context     string `json:"context"`     // Контекст: "завтрак", "ужин", "детям", "спорт" и т.д.
		MaxProducts int    `json:"maxProducts"` // Максимум товаров в ответе
	}
	type RecommendProductsOutput struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "recommend_products",
			Description: "Recommend products by context.",
		},
		func(ctx tool.Context, input RecommendProductsInput) (RecommendProductsOutput, error) {
			log.Printf("[TOOL] recommend_products called: context=%s, max=%d", input.Context, input.MaxProducts)

			if input.MaxProducts <= 0 {
				input.MaxProducts = 5
			}
			if input.MaxProducts > 10 {
				input.MaxProducts = 10
			}

			// Базовая логика рекомендаций на основе контекста
			searchQuery := input.Context

			// Расширяем поисковый запрос в зависимости от контекста
			contextMap := map[string]string{
				"завтрак":  "молоко яйца хлеб масло",
				"breakfast": "milk eggs bread butter",
				"ужин":     "мясо овощи",
				"dinner":   "meat vegetables",
				"здоровое": "овощи фрукты",
				"healthy":  "vegetables fruits",
				"детям":    "молоко йогурт фрукты",
				"kids":     "milk yogurt fruits",
				"вечеринка": "напитки закуски",
				"party":    "drinks snacks",
			}

			for key, expanded := range contextMap {
				if strings.Contains(strings.ToLower(input.Context), key) {
					searchQuery = expanded
					break
				}
			}

			products, err := storeClient.GetAllProducts(ctx, searchQuery)
			if err != nil {
				return RecommendProductsOutput{Result: fmt.Sprintf("Error getting recommendations: %v", err)}, nil
			}

			if len(products) == 0 {
				// Fallback - получаем любые товары
				products, _ = storeClient.GetAllProducts(ctx, "")
			}

			if len(products) > input.MaxProducts {
				products = products[:input.MaxProducts]
			}

			result := fmt.Sprintf("🎯 RECOMMENDATIONS for '%s'\n\n", input.Context)
			result += fmt.Sprintf("Here are %d products we suggest:\n\n", len(products))
			result += llm.FormatProductsList(products)

			return RecommendProductsOutput{Result: result}, nil
		},
	)
}

// createFindAlternativesTool - поиск аналогов товара
func createFindAlternativesTool(storeClient *llm.StoreClient) (tool.Tool, error) {
	type FindAlternativesInput struct {
		Product  string `json:"product"`  // Product name
		Criteria string `json:"criteria"` // Search criteria: "cheaper", "premium", "similar"
	}
	type FindAlternativesOutput struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "find_alternatives",
			Description: "Find alternative products.",
		},
		func(ctx tool.Context, input FindAlternativesInput) (FindAlternativesOutput, error) {
			log.Printf("[TOOL] find_alternatives called: product=%s, criteria=%s", input.Product, input.Criteria)

			if input.Product == "" {
				return FindAlternativesOutput{Result: "Error: product name is required"}, nil
			}

			// Ищем исходный товар
			originalProducts, err := storeClient.GetAllProducts(ctx, input.Product)
			if err != nil || len(originalProducts) == 0 {
				return FindAlternativesOutput{Result: fmt.Sprintf("Product '%s' not found.", input.Product)}, nil
			}

			original := originalProducts[0]

			// Определяем категорию для поиска аналогов
			searchQuery := ""
			if original.CategoryID != nil {
				// В идеале искать по категории, но у нас нет такого API
				// Используем название товара для поиска похожих
				searchQuery = strings.Split(original.NameRu, " ")[0] // Первое слово
			}

			// Получаем потенциальные аналоги
			alternatives, err := storeClient.GetAllProducts(ctx, searchQuery)
			if err != nil {
				return FindAlternativesOutput{Result: fmt.Sprintf("Error finding alternatives: %v", err)}, nil
			}

			// Фильтруем (убираем оригинальный товар)
			var filtered []llm.Product
			for _, p := range alternatives {
				if p.ID != original.ID {
					filtered = append(filtered, p)
				}
			}

			if len(filtered) == 0 {
				return FindAlternativesOutput{Result: "No alternatives found."}, nil
			}

			// Ограничиваем количество
			if len(filtered) > 8 {
				filtered = filtered[:8]
			}

			result := fmt.Sprintf("🔍 ALTERNATIVES to '%s'\n\n", original.NameRu)
			result += fmt.Sprintf("Original: %s - %s₾\n\n", original.NameRu, original.Price)
			result += fmt.Sprintf("Found %d alternatives:\n\n", len(filtered))
			result += llm.FormatProductsList(filtered)

			return FindAlternativesOutput{Result: result}, nil
		},
	)
}

// createGetProductsByCategoryTool - НАДЁЖНАЯ версия с поиском по category_id (включая подкатегории)
func createGetProductsByCategoryTool(storeClient *llm.StoreClient) (tool.Tool, error) {
	type GetProductsByCategoryInput struct {
		CategoryID int `json:"category_id"` // ID категории (получить через get_categories)
		Limit      int `json:"limit"`       // Лимит товаров
	}
	type GetProductsByCategoryOutput struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "get_products_by_category",
			Description: "Get products by category ID (includes subcategories). You MUST call get_categories first to find the category_id, then use it here.",
		},
		func(ctx tool.Context, input GetProductsByCategoryInput) (GetProductsByCategoryOutput, error) {
			log.Printf("[TOOL] get_products_by_category: category_id=%d", input.CategoryID)

			if input.CategoryID == 0 {
				return GetProductsByCategoryOutput{Result: "Error: category_id required. Call get_categories first to find the category ID."}, nil
			}

			if input.Limit <= 0 {
				input.Limit = 10
			}

			// Запрашиваем товары по category_id (API автоматически включит дочерние категории)
			products, err := storeClient.GetProductsByCategoryID(ctx, input.CategoryID)
			if err != nil {
				log.Printf("[TOOL] get_products_by_category error: %v", err)
				return GetProductsByCategoryOutput{Result: fmt.Sprintf("Error: %v", err)}, nil
			}

			if len(products) == 0 {
				log.Printf("[TOOL] get_products_by_category: found 0 products for category_id %d", input.CategoryID)
				result := fmt.Sprintf("No products found in category ID %d (including subcategories). The database does not have products in this category yet.", input.CategoryID)
				return GetProductsByCategoryOutput{Result: result}, nil
			}

			if len(products) > input.Limit {
				products = products[:input.Limit]
			}

			log.Printf("[TOOL] get_products_by_category: found %d products for category_id %d", len(products), input.CategoryID)

			// ===== TRACKING: Сохраняем интерес пользователя в SESSION =====
			state := ctx.State()
			state.Set(SessionKeyUserInterest, fmt.Sprintf("category_%d", input.CategoryID))
			state.Set(SessionKeyUserLastCategory, fmt.Sprintf("%d", input.CategoryID))
			log.Printf("[SESSION] 📝 Tracked user interest: category_id=%d", input.CategoryID)

			result := fmt.Sprintf("📂 Products in category %d:\n\n", input.CategoryID)
			result += llm.FormatProductsList(products)

			return GetProductsByCategoryOutput{Result: result}, nil
		},
	)
}
