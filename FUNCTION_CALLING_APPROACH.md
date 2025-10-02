# Function Calling: LLM сама решает что делать

## 🎯 Проблема текущего подхода

### Regex паттерны (❌ Плохо):
```go
// Нужно хардкодить ВСЕ возможные фразы:
{`какой\s*ассортимент`, ProductQueryList},
{`что\s*продаете`, ProductQueryList},
{`покажи\s*товар`, ProductQueryList},
// ... бесконечно
```

**Проблемы:**
- ❌ Забыли фразу → не работает ("что вообще продаете")
- ❌ Опечатка → не работает ("асортимент")
- ❌ Новая формулировка → не работает ("чем занимаетесь")
- ❌ Другой язык → нужно добавлять паттерны
- ❌ Сложно поддерживать (100+ паттернов)

---

## ✅ Решение: Function Calling (Gemini API)

### Как это работает:

```
1. LLM получает:
   ✓ Системный промпт: "You work in enddel store"
   ✓ Список доступных функций:
     - get_product_categories()
     - search_products(query)
   ✓ Сообщение клиента: "какой у вас асортимент?"

2. LLM САМА анализирует:
   "Клиент спрашивает про ассортимент → мне нужны категории
    → я вызову функцию get_product_categories()"

3. LLM возвращает:
   {
     "function_call": {
       "name": "get_product_categories",
       "arguments": {}
     }
   }

4. Система выполняет функцию:
   → API запрос к магазину
   → Получает реальные категории

5. LLM получает результат и отвечает клиенту:
   "У нас большой ассортимент! Категории:
    • Овощи и фрукты
    • Мясо и рыба
    ..."
```

### Никаких паттернов! LLM сама понимает!

---

## 🔧 Реализация

### 1. Объявляем функции (в коде)

```go
// gemini_client.go

func GetStoreFunctionTools() []GeminiTool {
    return []GeminiTool{{
        FunctionDeclarations: []GeminiFunctionDeclaration{
            {
                Name: "get_product_categories",
                Description: "Get list of all product categories. " +
                            "Use when customer asks about categories, " +
                            "types of products, assortment, catalog.",
                Parameters: map[string]interface{}{
                    "type": "object",
                    "properties": map[string]interface{}{},
                },
            },
            {
                Name: "search_products",
                Description: "Search products by name or category. " +
                            "Use when customer asks about specific product.",
                Parameters: map[string]interface{}{
                    "type": "object",
                    "properties": map[string]interface{}{
                        "query": map[string]interface{}{
                            "type": "string",
                            "description": "Search query",
                        },
                    },
                },
            },
        },
    }}
}
```

### 2. Передаем функции в Gemini API

```go
// GenerateResponse теперь передает Tools:

reqBody := GeminiRequest{
    Contents: geminiMessages,
    GenerationConfig: map[string]interface{}{
        "temperature": 0.7,
        "maxOutputTokens": 1000,
    },
    Tools: GetStoreFunctionTools(), // ← LLM знает о функциях!
}
```

### 3. Обрабатываем Function Call

```go
// LLM может вернуть или текст, или function_call

if response.HasFunctionCall() {
    // Выполняем функцию
    result := ExecuteFunction(response.FunctionCall)

    // Отправляем результат обратно LLM
    finalResponse := gemini.CallWithFunctionResult(result)

    return finalResponse
}

return response.Text
```

---

## 🌍 Преимущества

### 1. Универсальность
```
Клиент: "какой у вас асортимент" → LLM: вызываю get_categories()
Клиент: "what do you sell" → LLM: calling get_categories()
Клиент: "que vendes" → LLM: llamando get_categories()
```
**Никаких паттернов для каждого языка!**

### 2. Гибкость
```
Клиент: "чем вы занимаетесь?"
LLM понимает контекст: "Я работаю в магазине"
→ Вызывает get_categories() чтобы показать ассортимент
```

### 3. Интеллектуальность
```
Клиент: "есть ли у вас молоко?"
LLM: "Он ищет конкретный продукт"
→ Вызывает search_products(query="молоко")
```

### 4. Простота поддержки
```go
// Добавить новую функцию:

{
    Name: "check_product_availability",
    Description: "Check if product is in stock",
    Parameters: {
        "product_id": "string"
    }
}
```
**Всё!** LLM САМА поймет когда её использовать!

---

## 📊 Сравнение подходов

| Аспект | Regex паттерны | Function Calling |
|--------|----------------|------------------|
| Строк кода | 500+ | ~50 |
| Поддержка языков | Нужно добавлять для каждого | Автоматически все |
| Новые фразы | Не работают | Работают сразу |
| Опечатки | Не работают | LLM понимает |
| Контекстное понимание | Нет | Да |
| Поддержка | Сложная | Простая |

---

## 🚀 Пример реального диалога

### Вариант 1: Общий вопрос
```
User: "какой у вас асортимент в целом"

LLM thinks: "Клиент спрашивает про ассортимент"
→ Function call: get_product_categories()

System executes:
→ API call to store
→ Returns: ["Овощи", "Мясо", "Молочка", ...]

LLM responds:
"У нас большой ассортимент товаров!

📂 Наши категории:
  • Овощи и фрукты
  • Мясо и рыба
  • Молочные продукты
  • Хлеб и выпечка
  ...

Что вас интересует?"
```

### Вариант 2: Конкретный продукт
```
User: "есть ли молоко?"

LLM thinks: "Ищет конкретный продукт - молоко"
→ Function call: search_products(query="молоко")

System executes:
→ API call: /products?search=молоко
→ Returns: [
    {name: "Молоко 3.2%", price: "120₾", stock: 50},
    {name: "Молоко безлактозное", price: "150₾", stock: 30}
  ]

LLM responds:
"Да, есть молоко! 🥛

1. Молоко 3.2% - 120₾ (в наличии 50 шт.)
2. Молоко безлактозное - 150₾ (в наличии 30 шт.)

Что добавить в корзину?"
```

### Вариант 3: Косвенный вопрос
```
User: "чем вы занимаетесь?"

LLM thinks: "Клиент хочет узнать о магазине.
            Я работаю в магазине продуктов.
            Логично показать что продаем."
→ Function call: get_product_categories()

System executes:
→ Returns categories

LLM responds:
"Мы - онлайн-магазин доставки продуктов enddel! 🛒

Доставляем свежие продукты на дом:
  • Овощи и фрукты
  • Мясо и рыба
  • Молочные продукты
  ...

Доставка 1-3 часа, бесплатно от 1500₾. Что подобрать для вас?"
```

---

## 🔧 Что нужно сделать

### 1. Добавить структуры Function Calling
✅ **Уже сделано** в `gemini_client.go`

### 2. Обработать Function Call в ответе

```go
// В GeminiCandidate нужно добавить:

type GeminiCandidate struct {
    Content      GeminiMessage      `json:"content"`
    FunctionCall *GeminiFunctionCall `json:"functionCall,omitempty"`
    FinishReason string             `json:"finishReason"`
}

type GeminiFunctionCall struct {
    Name string                 `json:"name"`
    Args map[string]interface{} `json:"args"`
}
```

### 3. Создать обработчик функций

```go
func ExecuteStoreFunction(ctx context.Context, storeClient *StoreClient,
                          funcCall GeminiFunctionCall) (string, error) {
    switch funcCall.Name {
    case "get_product_categories":
        return executeGetCategories(ctx, storeClient)

    case "search_products":
        query := ""
        if q, ok := funcCall.Args["query"].(string); ok {
            query = q
        }
        return executeSearchProducts(ctx, storeClient, query)

    default:
        return "", fmt.Errorf("unknown function: %s", funcCall.Name)
    }
}
```

### 4. Интегрировать в AutoResponder

```go
// В ProcessMessage:

response, functionCall, err := ar.client.GenerateResponseWithTools(...)
if functionCall != nil {
    // Выполняем функцию
    result, err := ExecuteStoreFunction(ctx, ar.storeClient, functionCall)

    // Отправляем результат обратно LLM для финального ответа
    finalResponse, err := ar.client.ContinueWithFunctionResult(
        chatHistory,
        functionCall,
        result,
    )

    return finalResponse
}
```

### 5. Убрать regex паттерны!

```go
// Больше не нужно:
// - DetectProductQuery()
// - HandleProductQuery()
// - 100+ regex паттернов

// LLM САМА решает когда вызывать функции!
```

---

## 💡 Дополнительные функции

Можно добавить и другие функции:

```go
{
    Name: "get_order_status",
    Description: "Get status of customer's order by order ID",
    Parameters: {
        "order_id": "string"
    }
},
{
    Name: "check_delivery_zones",
    Description: "Check if delivery is available for address",
    Parameters: {
        "address": "string"
    }
},
{
    Name: "get_promotions",
    Description: "Get current promotions and special offers",
    Parameters: {}
}
```

LLM САМА поймет когда их вызывать!

---

## 🎯 Итого

### Было (regex):
```
100+ строк паттернов
Каждую фразу хардкодить
Каждый язык добавлять
Опечатки не работают
```

### Стало (function calling):
```
2 функции
LLM сама понимает
Все языки автоматически
Опечатки и контекст работают
```

### Код сокращается с 500+ строк до ~100 строк!

---

**Файлы для изменения:**
1. ✅ `gemini_client.go` - добавлены структуры
2. ✅ `gemini_client.go` - добавлена обработка functionCall в ответе
3. ✅ `autoresponder.go` - интегрирован function calling
4. ⏳ `product_handler.go` - можно удалить regex паттерны (опционально)

---

## ✅ РЕАЛИЗОВАНО

### Что сделано:

1. **gemini_client.go:**
   - ✅ Добавлена структура `GeminiFunctionCall`
   - ✅ Добавлен метод `GenerateResponseWithTools()` - отправляет запрос с tools и возвращает либо текст, либо function call
   - ✅ Добавлен метод `ContinueWithFunctionResult()` - отправляет результат функции обратно LLM для финального ответа
   - ✅ Добавлены вспомогательные функции `marshalArgs()` и `quote()`

2. **tools.go:**
   - ✅ Функции `ExecuteTool()`, `executeGetProducts()`, `executeGetCategories()`, `executeSearchProduct()` уже были реализованы
   - ✅ Очищены дубликаты форматирования (используются функции из product_handler.go)

3. **autoresponder.go:**
   - ✅ Обновлен интерфейс `LLM` с новыми методами
   - ✅ В `ProcessMessage()` заменен вызов `GenerateResponse()` на `GenerateResponseWithTools()`
   - ✅ Добавлена обработка function call: выполнение функции и получение финального ответа
   - ✅ Добавлено детальное логирование function calling

### Как это работает сейчас:

```
1. Клиент: "какой у вас асортимент"
   ↓
2. AutoResponder вызывает GenerateResponseWithTools() с tools
   ↓
3. LLM анализирует: "клиент спрашивает про ассортимент"
   → Возвращает functionCall: {name: "get_product_categories", args: {}}
   ↓
4. AutoResponder выполняет ExecuteTool()
   → Запрос к store API: GetAllCategories()
   → Получает реальные категории
   ↓
5. AutoResponder вызывает ContinueWithFunctionResult() с результатом
   ↓
6. LLM получает данные и формирует финальный ответ:
   "У нас большой ассортимент товаров!
   📂 Наши категории:
     • [Реальная категория 1]
     • [Реальная категория 2]
     ..."
   ↓
7. Клиент получает ответ с РЕАЛЬНЫМИ данными из API
```

### Преимущества реализации:

✅ **LLM сама решает когда вызывать функции** - не нужно хардкодить regex паттерны
✅ **Работает на всех языках** - LLM понимает запросы на любом языке
✅ **Контекстное понимание** - LLM понимает косвенные вопросы ("чем занимаетесь?")
✅ **Опечатки и вариации** - LLM понимает "асортимент", "ассортимент", "что продаете"
✅ **Простота поддержки** - добавить новую функцию = добавить одно объявление в `GetStoreFunctionTools()`

---

**Автор:** Claude Code
**Дата:** 2025-10-02
**Статус:** ✅ ПОЛНОСТЬЮ РЕАЛИЗОВАНО и готово к деплою
