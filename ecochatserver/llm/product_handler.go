package llm

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
)

// ProductQueryType представляет тип запроса о продукте
type ProductQueryType int

const (
	ProductQueryUnknown ProductQueryType = iota
	ProductQuerySearch                   // Поиск продукта по названию
	ProductQueryList                     // Список всех продуктов
	ProductQueryInfo                     // Информация о конкретном продукте
)

// ProductQuery представляет распознанный запрос о продукте
type ProductQuery struct {
	Type       ProductQueryType
	SearchTerm string // Поисковый запрос
	Category   string // Категория товаров
}

// DetectProductQuery пытается распознать запрос о продуктах в сообщении пользователя
func DetectProductQuery(message string) *ProductQuery {
	msgLower := strings.ToLower(message)

	// Паттерны для распознавания запросов о продуктах (мультиязычные)
	// ВАЖНО: Порядок имеет значение! Более специфичные паттерны должны быть первыми
	patterns := []struct {
		pattern string
		qtype   ProductQueryType
	}{
		// ═══ РУССКИЙ - ОБЩИЕ ЗАПРОСЫ (приоритет!) ═══
		{`какие\s*(?:вообще[тм]?|именно)?\s*(?:у\s*вас)?\s*(?:товары?|продукты?)\s*(?:есть|имеются?)?(?:\s+в\s+наличии)?`, ProductQueryList},
		{`что\s*у\s*вас\s*есть`, ProductQueryList},
		{`покажи\s*(?:товар|продукт|ассортимент)`, ProductQueryList},
		{`что\s*продаете`, ProductQueryList},
		{`какой\s*ассортимент`, ProductQueryList},
		{`ассортимент`, ProductQueryList},
		{`каталог`, ProductQueryList},

		// ═══ ENGLISH - ОБЩИЕ ЗАПРОСЫ (приоритет!) ═══
		{`what\s*(?:products?|items?)?\s*(?:do\s*you)?\s*have(?:\s+in\s+your\s+store)?`, ProductQueryList},
		{`(?:show|list)\s*(?:me\s*)?(?:your\s*)?(?:products?|items?|catalog)`, ProductQueryList},
		{`what\s*(?:do\s*you)?\s*sell`, ProductQueryList},
		{`your\s*(?:products?|catalog|assortment)`, ProductQueryList},
		{`(?:i\s*)?can'?t\s*(?:seem\s*to\s*)?(?:find|figure)`, ProductQueryList},

		// ═══ PORTUGUÊS - ОБЩИЕ ЗАПРОСЫ (приоритет!) ═══
		{`o\s*que\s*(?:você|vocês)\s*tem`, ProductQueryList},
		{`quais\s*(?:são\s*)?(?:os\s*)?(?:produtos?|items?)`, ProductQueryList},
		{`(?:mostre|lista)\s*(?:me\s*)?(?:o\s*)?(?:catálogo|produtos?)`, ProductQueryList},
		{`o\s*que\s*(?:você|vocês)\s*vende`, ProductQueryList},
		{`seu\s*catálogo`, ProductQueryList},

		// ═══ РУССКИЙ - ПОИСК КОНКРЕТНЫХ ТОВАРОВ ═══
		{`(?:можите|можете|могли\s*бы(?:\s+вы)?)\s+(?:пожалуйста\s+)?(?:рассказать|расказать)\s+(?:пожалуйста\s+)?(?:про|о|подробнее\s+про)\s+(.+)`, ProductQuerySearch},
		{`(?:расскажите|раскажите)\s+(?:пожалуйста\s+)?(?:про|о|подробнее\s+про)\s+(.+)`, ProductQuerySearch},
		{`подробнее\s+(?:пожалуйста\s+)?(?:про|о)\s+(.+)`, ProductQuerySearch},
		{`что\s+(?:это|такое)\s+(.+)`, ProductQuerySearch},
		{`ищу\s+(.+)`, ProductQuerySearch},
		{`есть\s+ли\s+(.+)`, ProductQuerySearch},
		{`у\s*вас\s*есть\s+(.+)`, ProductQuerySearch},
		{`хочу\s+(?:купить\s+)?(.+)`, ProductQuerySearch},
		{`мне\s+нужн(?:о|а|ы)\s+(.+)`, ProductQuerySearch},

		// ═══ ENGLISH - ПОИСК КОНКРЕТНЫХ ТОВАРОВ ═══
		{`(?:tell|could\s+you\s+tell)\s+me\s+(?:more\s+)?about\s+(.+)`, ProductQuerySearch},
		{`(?:more\s+)?(?:details|info(?:rmation)?)\s+(?:about|on)\s+(.+)`, ProductQuerySearch},
		{`what\s+(?:is|are)\s+(.+)`, ProductQuerySearch},
		{`looking\s*for\s+(.+)`, ProductQuerySearch},
		{`do\s*you\s*have\s+(.+)`, ProductQuerySearch},
		{`i\s*(?:want|need)\s+(?:to\s*buy\s+)?(.+)`, ProductQuerySearch},
		{`show\s*me\s+(.+)`, ProductQuerySearch},

		// ═══ PORTUGUÊS - ПОИСК КОНКРЕТНЫХ ТОВАРОВ ═══
		{`(?:me\s+)?(?:conte|fale)\s+(?:mais\s+)?sobre\s+(.+)`, ProductQuerySearch},
		{`(?:mais\s+)?(?:detalhes|informações)\s+sobre\s+(.+)`, ProductQuerySearch},
		{`o\s+que\s+(?:é|são)\s+(.+)`, ProductQuerySearch},
		{`procurando\s+(?:por\s+)?(.+)`, ProductQuerySearch},
		{`(?:você|vocês)\s*tem\s+(.+)`, ProductQuerySearch},
		{`(?:eu\s*)?(?:quero|preciso)\s+(?:de\s+)?(.+)`, ProductQuerySearch},
	}

	for _, p := range patterns {
		re := regexp.MustCompile(p.pattern)
		if matches := re.FindStringSubmatch(msgLower); matches != nil {
			query := &ProductQuery{Type: p.qtype}

			// Если нашли поисковый термин
			if len(matches) > 1 && matches[len(matches)-1] != "" {
				query.SearchTerm = strings.TrimSpace(matches[len(matches)-1])
				// Очищаем от знаков препинания в конце
				query.SearchTerm = strings.TrimRight(query.SearchTerm, "?!.,;:")
				query.SearchTerm = strings.TrimSpace(query.SearchTerm)

				if query.Type == ProductQueryList {
					query.Type = ProductQuerySearch
				}
			}

			log.Printf("[PRODUCT_HANDLER] Распознан запрос: type=%d, term='%s'", query.Type, query.SearchTerm)
			return query
		}
	}

	return nil
}

// HandleProductQuery обрабатывает запрос о продукте и возвращает ответ
func HandleProductQuery(ctx context.Context, storeClient *StoreClient, query *ProductQuery) (string, error) {
	if storeClient == nil {
		return "", fmt.Errorf("store client not initialized")
	}

	log.Printf("[PRODUCT_HANDLER] Обработка запроса: type=%d, searchTerm='%s'", query.Type, query.SearchTerm)

	switch query.Type {
	case ProductQueryList:
		return handleProductsList(ctx, storeClient)

	case ProductQuerySearch:
		if query.SearchTerm != "" {
			return handleProductSearch(ctx, storeClient, query.SearchTerm)
		}
		return handleProductsList(ctx, storeClient)

	default:
		return "", fmt.Errorf("unknown product query type")
	}
}

// handleProductsList получает список популярных/рекомендуемых продуктов
// Показывает категории для удобства навигации
func handleProductsList(ctx context.Context, storeClient *StoreClient) (string, error) {
	log.Printf("[PRODUCT_HANDLER] Запрос общего списка продуктов")

	// Получаем категории для навигации
	categories, err := storeClient.GetAllCategories(ctx)
	if err != nil {
		log.Printf("[PRODUCT_HANDLER] Ошибка получения категорий: %v", err)
		// Продолжаем без категорий
	}

	var response strings.Builder
	response.WriteString("У нас большой ассортимент товаров!\n\n")

	// Показываем категории, если есть
	if len(categories) > 0 {
		response.WriteString("📂 Наши категории:\n")
		for i, cat := range categories {
			if i >= 10 { // Ограничиваем 10 категориями
				break
			}
			response.WriteString(fmt.Sprintf("  • %s\n", cat.NameRu))
		}
		response.WriteString("\nНапишите название категории или конкретного товара, который ищете!\n")
	} else {
		// Если категорий нет, показываем несколько товаров
		products, err := storeClient.GetAllProducts(ctx, "")
		if err != nil {
			log.Printf("[PRODUCT_HANDLER] Ошибка получения продуктов: %v", err)
			return "Извините, произошла ошибка при получении списка товаров. Попробуйте позже.", nil
		}

		if len(products) == 0 {
			return "К сожалению, в данный момент товары отсутствуют.", nil
		}

		// Ограничиваем до 15 товаров для общего списка
		if len(products) > 15 {
			products = products[:15]
		}

		response.WriteString("Вот некоторые из них:\n\n")
		response.WriteString(FormatProductsList(products))
		response.WriteString("\n\nЧто именно вас интересует? Я могу помочь найти конкретный товар.")
	}

	return response.String(), nil
}

// findCategoryByName ищет категорию по названию на любом языке (регистронезависимо)
func findCategoryByName(ctx context.Context, storeClient *StoreClient, searchTerm string) (*Category, error) {
	categories, err := storeClient.GetAllCategories(ctx)
	if err != nil {
		return nil, err
	}

	searchLower := strings.ToLower(searchTerm)

	for _, cat := range categories {
		// Проверяем совпадение с любым названием категории
		if strings.Contains(strings.ToLower(cat.NameRu), searchLower) ||
			strings.Contains(strings.ToLower(cat.NameEn), searchLower) ||
			strings.Contains(strings.ToLower(cat.NamePt), searchLower) ||
			strings.Contains(strings.ToLower(cat.NameEs), searchLower) {
			return &cat, nil
		}
	}

	return nil, nil // Категория не найдена
}

// handleProductSearch ищет продукты по поисковому запросу через API магазина
// Умный поиск: сначала проверяет категории, потом ищет по названию
func handleProductSearch(ctx context.Context, storeClient *StoreClient, searchTerm string) (string, error) {
	log.Printf("[PRODUCT_HANDLER] Целевой поиск продуктов через API: term='%s'", searchTerm)

	// Шаг 1: Проверяем, не является ли это названием категории
	category, err := findCategoryByName(ctx, storeClient, searchTerm)
	if err != nil {
		log.Printf("[PRODUCT_HANDLER] Ошибка поиска категории: %v", err)
		// Продолжаем обычный поиск
	}

	if category != nil {
		log.Printf("[PRODUCT_HANDLER] Найдена категория: ID=%d, NameRu='%s'", category.ID, category.NameRu)
		// Поиск товаров этой категории через API
		// API /products?search=term ищет по названию, но мы можем передать название категории
		matchedProducts, err := storeClient.GetAllProducts(ctx, category.NameRu)
		if err != nil {
			log.Printf("[PRODUCT_HANDLER] Ошибка поиска товаров по категории: %v", err)
			return "Извините, произошла ошибка при поиске товаров. Попробуйте позже.", nil
		}

		if len(matchedProducts) > 0 {
			// Ограничиваем до 10 товаров
			if len(matchedProducts) > 10 {
				matchedProducts = matchedProducts[:10]
			}
			return fmt.Sprintf("Товары в категории \"%s\":\n\n%s", category.NameRu, FormatProductsList(matchedProducts)), nil
		}
	}

	// Шаг 2: Обычный поиск по названию/описанию
	matchedProducts, err := storeClient.GetAllProducts(ctx, searchTerm)
	if err != nil {
		log.Printf("[PRODUCT_HANDLER] Ошибка поиска продуктов: %v", err)
		return "Извините, произошла ошибка при поиске товаров. Попробуйте позже.", nil
	}

	if len(matchedProducts) == 0 {
		return fmt.Sprintf("К сожалению, по запросу '%s' ничего не найдено. Попробуйте уточнить запрос или спросите про другие товары.", searchTerm), nil
	}

	// Если найден ровно один товар - показываем детальную информацию
	if len(matchedProducts) == 1 {
		return FormatProductDetails(matchedProducts[0]), nil
	}

	// Ограничиваем до 10 товаров для читаемости
	if len(matchedProducts) > 10 {
		matchedProducts = matchedProducts[:10]
	}

	return fmt.Sprintf("Найдено несколько товаров по запросу '%s':\n\n%s\n\nУкажите более точное название для получения подробной информации.", searchTerm, FormatProductsList(matchedProducts)), nil
}

// FormatProductDetails форматирует детальную информацию о товаре
func FormatProductDetails(product Product) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("🛒 **%s**\n\n", product.NameRu))

	if product.Description != "" {
		sb.WriteString(fmt.Sprintf("📝 Описание:\n%s\n\n", product.Description))
	}

	if product.Price != "" && product.Price != "0" && product.Price != "0.00" {
		sb.WriteString(fmt.Sprintf("💰 Цена: **%s₾**\n", product.Price))
	}

	if product.StockQuantity > 0 || product.InStock {
		sb.WriteString(fmt.Sprintf("✅ **В наличии** (доступно: %d)\n", product.StockQuantity))
	} else {
		sb.WriteString("❌ Нет в наличии\n")
	}

	sb.WriteString("\nДля оформления заказа добавьте товар в корзину на сайте [enddel.com](https://enddel.com).")

	return sb.String()
}

// FormatProductsList форматирует список продуктов для отображения
func FormatProductsList(products []Product) string {
	if len(products) == 0 {
		return "Товары не найдены."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Найдено товаров: %d\n\n", len(products)))

	for i, p := range products {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, p.NameRu))

		if p.Price != "" && p.Price != "0" && p.Price != "0.00" {
			sb.WriteString(fmt.Sprintf("   Цена: %s₾\n", p.Price))
		}

		if p.Description != "" {
			// Ограничиваем описание 100 символами
			desc := p.Description
			if len(desc) > 100 {
				desc = desc[:97] + "..."
			}
			sb.WriteString(fmt.Sprintf("   %s\n", desc))
		}

		if p.StockQuantity > 0 || p.InStock {
			sb.WriteString(fmt.Sprintf("   ✓ В наличии (%d шт.)\n", p.StockQuantity))
		} else {
			sb.WriteString("   ✗ Нет в наличии\n")
		}

		sb.WriteString("\n")
	}

	sb.WriteString("Для оформления заказа вы можете добавить товары в корзину на сайте.")
	return sb.String()
}
