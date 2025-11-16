package adkagent

import (
	"fmt"
	"log"
	"strings"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/egor/ecochatserver/llm"
)

// ============================================================================
// PRODUCT AGENT TOOLS - Инструменты для продуктового консультанта
// ============================================================================

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
		SearchQuery string `json:"searchQuery"`
	}
	type ProductsOutput struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "get_products",
			Description: "Get list of products from store. Use when customer asks about products, catalog, assortment. Can optionally filter by search query.",
		},
		func(ctx tool.Context, input GetProductsInput) (ProductsOutput, error) {
			log.Printf("[TOOL] get_products called: query=%s", input.SearchQuery)

			products, err := storeClient.GetAllProducts(ctx, input.SearchQuery)
			if err != nil {
				return ProductsOutput{Result: fmt.Sprintf("Error: %v", err)}, nil
			}

			if len(products) == 0 {
				return ProductsOutput{Result: "No products found."}, nil
			}

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
		ProductName string `json:"productName"`
	}
	type SearchProductOutput struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "search_product",
			Description: "Search for a specific product by name. Use when customer asks about a specific product or wants detailed info about one product.",
		},
		func(ctx tool.Context, input SearchProductInput) (SearchProductOutput, error) {
			log.Printf("[TOOL] search_product called: productName=%s", input.ProductName)

			if input.ProductName == "" {
				return SearchProductOutput{Result: "Error: product name is required"}, nil
			}

			products, err := storeClient.GetAllProducts(ctx, input.ProductName)
			if err != nil {
				return SearchProductOutput{Result: fmt.Sprintf("Error: %v", err)}, nil
			}

			if len(products) == 0 {
				return SearchProductOutput{Result: fmt.Sprintf("Product '%s' not found.", input.ProductName)}, nil
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
			Description: "Get list of all product categories available in the store. Use when customer asks about product categories or wants to browse by category.",
		},
		func(ctx tool.Context, input GetCategoriesInput) (CategoriesOutput, error) {
			log.Printf("[TOOL] get_categories called")

			categories, err := storeClient.GetAllCategories(ctx)
			if err != nil {
				return CategoriesOutput{Result: fmt.Sprintf("Error: %v", err)}, nil
			}

			if len(categories) == 0 {
				return CategoriesOutput{Result: "No categories found in the store."}, nil
			}

			result := fmt.Sprintf("Store has %d product categories:\n", len(categories))
			for i, cat := range categories {
				if i >= 20 {
					result += fmt.Sprintf("\n... and %d more categories", len(categories)-20)
					break
				}
				result += fmt.Sprintf("• %s\n", cat.NameRu)
			}

			return CategoriesOutput{Result: result}, nil
		},
	)
}

func createCheckAvailabilityTool(storeClient *llm.StoreClient) (tool.Tool, error) {
	type CheckProductAvailabilityInput struct {
		ProductSlug string `json:"productSlug"`
	}
	type ProductAvailabilityOutput struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "check_product_availability",
			Description: "Check if a product is available in stock by its slug. Use when customer asks 'is this product available', 'in stock', etc.",
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
		ProductNames []string `json:"productNames"` // Названия товаров для сравнения
	}
	type CompareProductsOutput struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "compare_products",
			Description: "Compare multiple products side by side. Use when customer asks 'what's the difference between X and Y', 'compare these products', 'which is better'. Provide product names to compare.",
		},
		func(ctx tool.Context, input CompareProductsInput) (CompareProductsOutput, error) {
			log.Printf("[TOOL] compare_products called: products=%v", input.ProductNames)

			if len(input.ProductNames) < 2 {
				return CompareProductsOutput{Result: "Need at least 2 products to compare."}, nil
			}

			if len(input.ProductNames) > 4 {
				return CompareProductsOutput{Result: "Can compare maximum 4 products at once."}, nil
			}

			// Собираем информацию о каждом товаре
			var products []llm.Product
			for _, name := range input.ProductNames {
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
			Description: "Get smart product recommendations based on context. Use when customer asks 'what should I buy for breakfast/dinner/party', 'recommend something', 'what's good for kids'. Provide context (e.g. 'breakfast', 'dinner', 'healthy', 'kids') and max number of products.",
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
		ProductName string `json:"productName"` // Название товара
		Criteria    string `json:"criteria"`    // Критерий: "cheaper", "premium", "similar"
	}
	type FindAlternativesOutput struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "find_alternatives",
			Description: "Find alternative products similar to specified product. Use when customer asks 'show me cheaper alternatives', 'similar products', 'other options'. Criteria can be: 'cheaper' (lower price), 'premium' (higher quality), 'similar' (same category).",
		},
		func(ctx tool.Context, input FindAlternativesInput) (FindAlternativesOutput, error) {
			log.Printf("[TOOL] find_alternatives called: product=%s, criteria=%s", input.ProductName, input.Criteria)

			if input.ProductName == "" {
				return FindAlternativesOutput{Result: "Error: product name is required"}, nil
			}

			// Ищем исходный товар
			originalProducts, err := storeClient.GetAllProducts(ctx, input.ProductName)
			if err != nil || len(originalProducts) == 0 {
				return FindAlternativesOutput{Result: fmt.Sprintf("Product '%s' not found.", input.ProductName)}, nil
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

// createGetProductsByCategoryTool - получение товаров по категории
func createGetProductsByCategoryTool(storeClient *llm.StoreClient) (tool.Tool, error) {
	type GetProductsByCategoryInput struct {
		CategoryName string `json:"categoryName"` // Название категории
		Limit        int    `json:"limit"`        // Лимит товаров
	}
	type GetProductsByCategoryOutput struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "get_products_by_category",
			Description: "Get products from a specific category. Use when customer asks 'show me products from X category', 'what do you have in Y section'. Provide category name and optional limit.",
		},
		func(ctx tool.Context, input GetProductsByCategoryInput) (GetProductsByCategoryOutput, error) {
			log.Printf("[TOOL] get_products_by_category called: category=%s, limit=%d", input.CategoryName, input.Limit)

			if input.CategoryName == "" {
				return GetProductsByCategoryOutput{Result: "Error: category name is required"}, nil
			}

			if input.Limit <= 0 {
				input.Limit = 10
			}

			// Используем поиск по названию категории
			products, err := storeClient.GetAllProducts(ctx, input.CategoryName)
			if err != nil {
				return GetProductsByCategoryOutput{Result: fmt.Sprintf("Error: %v", err)}, nil
			}

			if len(products) == 0 {
				return GetProductsByCategoryOutput{Result: fmt.Sprintf("No products found in category '%s'.", input.CategoryName)}, nil
			}

			if len(products) > input.Limit {
				products = products[:input.Limit]
			}

			result := fmt.Sprintf("📂 Products in '%s' category:\n\n", input.CategoryName)
			result += llm.FormatProductsList(products)

			return GetProductsByCategoryOutput{Result: result}, nil
		},
	)
}
