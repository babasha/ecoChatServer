# Оптимизация Tool Descriptions

## Текущая проблема

Tool descriptions слишком verbose, используют много токенов:

```go
// СЕЙЧАС (~60 токенов):
Description: "Get list of products from store. Use when customer asks about products, catalog, assortment. Can optionally filter by 'query' or 'category' parameter (product name, category, etc). Use 'all' to get all products."
```

---

## Оптимизация (сохраняем функциональность)

### БЫЛО → СТАЛО

**get_products:**
```go
// БЫЛО (58 слов):
"Get list of products from store. Use when customer asks about products, catalog, assortment. Can optionally filter by 'query' or 'category' parameter (product name, category, etc). Use 'all' to get all products."

// СТАЛО (8 слов):
"Get products. Filter by query or category."
```

**search_product:**
```go
// БЫЛО (35 слов):
"Search for a specific product by name. Use when customer asks about a specific product or wants detailed info about one product. Provide search query in 'query' parameter."

// СТАЛО (6 слов):
"Search product by name."
```

**get_categories:**
```go
// БЫЛО (27 слов):
"Get list of all product categories available in the store. Use when customer asks about product categories or wants to browse by category."

// СТАЛО (4 слова):
"Get all categories."
```

**check_product_availability:**
```go
// БЫЛО (21 слов):
"Check if a product is available in stock by its slug. Use when customer asks 'is this product available', 'in stock', etc."

// СТАЛО (5 слов):
"Check product stock by slug."
```

---

## Правило оптимизации:

LLM понимает что делает tool из:
1. **Названия tool** - `get_categories` уже понятно что делает
2. **Названий параметров** - `{category: string, limit: int}` самоочевидно
3. **Краткого description** - 3-8 слов достаточно

**НЕ НУЖНО:**
- ❌ Объяснять когда использовать tool ("Use when customer asks...")
- ❌ Давать примеры в description
- ❌ Повторять названия параметров в тексте

---

## Итоговая экономия

| Tool | Было токенов | Стало токенов | Экономия |
|------|--------------|---------------|----------|
| get_products | ~60 | ~10 | -83% |
| search_product | ~35 | ~8 | -77% |
| get_categories | ~27 | ~6 | -78% |
| check_product_availability | ~21 | ~7 | -67% |
| compare_products | ~40 | ~8 | -80% |
| recommend_products | ~45 | ~10 | -78% |
| find_alternatives | ~38 | ~9 | -76% |
| get_products_by_category | ~32 | ~8 | -75% |

**Итого для 8 product tools:**
- Было: ~298 токенов
- Стало: ~66 токенов
- **Экономия: 232 токена (78%)**

**Для всех 19 tools:**
- Было: ~800 токенов
- Стало: ~200 токенов
- **Экономия: 600 токенов (75%)**

---

## Пример implementation

```go
func createGetProductsTool(storeClient *llm.StoreClient) (tool.Tool, error) {
    type GetProductsInput struct {
        Query    string `json:"query,omitempty"`
        Category string `json:"category,omitempty"`
    }
    type ProductsOutput struct {
        Result string `json:"result"`
    }

    return functiontool.New(
        functiontool.Config{
            Name:        "get_products",
            // БЫЛО:
            // Description: "Get list of products from store. Use when customer asks about products, catalog, assortment. Can optionally filter by 'query' or 'category' parameter (product name, category, etc). Use 'all' to get all products.",

            // СТАЛО:
            Description: "Get products. Filter by query or category.",
        },
        // ... function implementation
    )
}
```

---

## Тестирование

1. Замените descriptions на короткие
2. Запустите 50-100 тестовых запросов
3. Проверьте что LLM все еще правильно выбирает tools
4. Если качество упало - добавьте 1-2 ключевых слова

**Примеры тестовых запросов:**
- "Покажи все товары" → должен вызвать `get_products`
- "Что у вас есть попить?" → должен вызвать `get_categories` → `get_products_by_category`
- "Есть ли вино в наличии?" → должен вызвать `search_product` + `check_product_availability`

---

## Trade-offs

✅ **Плюсы:**
- Экономия ~600 токенов (75%)
- Быстрее обработка
- Дешевле

⚠️ **Потенциальные риски:**
- LLM может реже выбирать правильный tool (проверить в тестах)
- Для сложных tools может понадобиться чуть более длинное описание

**Решение:** Начните с агрессивного сокращения, потом постепенно добавляйте слова если нужно.
