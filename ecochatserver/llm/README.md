# LLM Provider Architecture

## Обзор

Универсальная архитектура для работы с разными LLM провайдерами (Gemini, OpenAI, Claude, Ollama и др.).
Позволяет легко переключаться между провайдерами без изменения бизнес-логики.

## Архитектура

```
┌─────────────────────────────────────┐
│      autoresponder.go               │
│   (бизнес-логика)                   │
└────────────┬────────────────────────┘
             │ использует
             ▼
┌─────────────────────────────────────┐
│      Provider Interface             │
│  (универсальные методы)             │
└────────────┬────────────────────────┘
             │ реализуют
      ┌──────┴──────┬─────────┐
      ▼             ▼         ▼
┌──────────┐  ┌──────────┐  ┌──────────┐
│ Gemini   │  │ OpenAI   │  │ Claude   │
│ Adapter  │  │ Adapter  │  │ Adapter  │
└──────────┘  └──────────┘  └──────────┘
```

## Компоненты

### 1. Universal Types (`provider_types.go`)

Универсальные типы данных, не зависящие от конкретного провайдера:

- **Message** - сообщение в диалоге
- **Tool** - описание функции для LLM
- **FunctionCall** - вызов функции от LLM
- **Response** - ответ от провайдера
- **Provider** - интерфейс провайдера

### 2. Provider Factory (`provider_factory.go`)

Фабрика для создания провайдеров на основе конфигурации:

```go
// Создание провайдера из переменных окружения
provider, err := llm.NewProvider(nil)

// Создание провайдера с кастомной конфигурацией
config := &llm.ProviderConfig{
    Type:    llm.ProviderGemini,
    APIKey:  "your-api-key",
    Model:   "gemini-2.5-flash",
    Timeout: 30,
}
provider, err := llm.NewProvider(config)
```

### 3. Adapters

Адаптеры для конкретных провайдеров:

- **GeminiAdapter** (`gemini_adapter.go`) - адаптер для Google Gemini
- **OpenAI Adapter** - TODO
- **Claude Adapter** - TODO
- **Ollama Adapter** - TODO

### 4. Provider Interface

Все провайдеры реализуют универсальный интерфейс:

```go
type Provider interface {
    GetName() string
    GenerateResponse(ctx, userMessage, chatHistory, opts) (*Response, error)
    GenerateWithTools(ctx, userMessage, chatHistory, tools, opts) (*Response, error)
    ContinueWithFunctionResult(ctx, chatHistory, functionCall, result, opts) (*Response, error)
    TranslateText(ctx, text, fromLang, toLang) (string, error)
    DetectAndTranslate(ctx, text, targetLang) (*TranslationResult, error)
    TranslateBatch(ctx, texts, fromLang, toLang) ([]string, error)
}
```

## Использование

### Быстрый старт

По умолчанию используется Gemini. Для переключения на другой провайдер установите переменные окружения:

```bash
# Выбор провайдера
export LLM_PROVIDER=gemini  # или openai, claude, ollama

# Настройки для Gemini
export GEMINI_API_KEY=your-key-here
export GEMINI_MODEL=gemini-2.5-flash

# Настройки для OpenAI (TODO)
export OPENAI_API_KEY=your-key-here
export OPENAI_MODEL=gpt-4o-mini

# Настройки для Claude (TODO)
export ANTHROPIC_API_KEY=your-key-here
export CLAUDE_MODEL=claude-3-5-sonnet-20241022

# Настройки для Ollama (TODO)
export OLLAMA_BASE_URL=http://localhost:11434
export OLLAMA_MODEL=llama3.1

# Общие настройки
export LLM_API_TIMEOUT=30  # таймаут в секундах
```

### Пример использования в коде

```go
// Создание провайдера
provider, err := llm.NewProvider(nil) // nil = использовать env
if err != nil {
    log.Fatal(err)
}

// Создание AutoResponder
cfg := llm.GetDefaultConfig()
autoResponder := llm.NewAutoResponder(provider, cfg)

// Или проще - создать всё сразу из env
autoResponder, err := llm.NewAutoResponderWithConfig(cfg)
```

### Работа с Function Calling

```go
// Получение универсальных инструментов для магазина
tools := llm.GetUniversalStoreFunctionTools()

// Генерация ответа с поддержкой function calling
response, err := provider.GenerateWithTools(
    ctx,
    userMessage,
    chatHistory,
    tools,
    &llm.GenerateOptions{
        Temperature: 0.7,
        MaxTokens:   1000,
    },
)

if response.FunctionCall != nil {
    // Выполнить функцию
    result := executeTool(response.FunctionCall)

    // Отправить результат обратно
    finalResponse, err := provider.ContinueWithFunctionResult(
        ctx,
        chatHistory,
        response.FunctionCall,
        result,
        opts,
    )
}
```

## Миграция с старого кода

### Было (Gemini-специфичный код):

```go
client := llm.NewGeminiClient()
AutoResponder = llm.NewAutoResponder(client, cfg)
tools := llm.GetStoreFunctionTools() // Возвращал []GeminiTool
```

### Стало (универсальный код):

```go
provider, _ := llm.NewProvider(nil)
AutoResponder = llm.NewAutoResponder(provider, cfg)
tools := llm.GetUniversalStoreFunctionTools() // Возвращает []Tool
```

## Добавление нового провайдера

Для добавления поддержки нового провайдера:

1. Создайте файл адаптера (например, `openai_adapter.go`)
2. Реализуйте интерфейс `Provider`
3. Добавьте создание адаптера в `provider_factory.go`
4. Добавьте новый тип в `ProviderType` enum
5. Обновите документацию

### Пример структуры адаптера:

```go
type OpenAIAdapter struct {
    client  *OpenAIClient
    apiKey  string
    model   string
    timeout time.Duration
}

func (a *OpenAIAdapter) Initialize() error {
    // Инициализация клиента
}

func (a *OpenAIAdapter) GetName() string {
    return fmt.Sprintf("OpenAI (%s)", a.model)
}

func (a *OpenAIAdapter) GenerateResponse(...) (*Response, error) {
    // Реализация генерации
}

// ... остальные методы интерфейса Provider
```

## Преимущества новой архитектуры

✅ **Универсальность** - легко переключаться между провайдерами
✅ **Расширяемость** - просто добавлять новые провайдеры
✅ **Тестируемость** - можно мокировать Provider интерфейс
✅ **Поддерживаемость** - бизнес-логика не зависит от конкретного API
✅ **Конфигурируемость** - провайдер выбирается через env переменные

## Текущий статус

- ✅ Gemini Provider - полностью реализован
- ⏳ OpenAI Provider - TODO
- ⏳ Claude Provider - TODO
- ⏳ Ollama Provider - TODO

## Troubleshooting

### Ошибка "API key is required"

Проверьте, что установлены необходимые переменные окружения для выбранного провайдера.

### Ошибка "unknown provider type"

Убедитесь, что переменная `LLM_PROVIDER` установлена корректно (gemini/openai/claude/ollama).

### Провайдер не переключается

Перезапустите сервер после изменения переменных окружения.

## Дополнительная информация

- [Gemini API Documentation](https://ai.google.dev/docs)
- [OpenAI API Documentation](https://platform.openai.com/docs)
- [Anthropic Claude API](https://docs.anthropic.com/)
- [Ollama Documentation](https://ollama.ai/docs)
