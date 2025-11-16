# 🛡️ Защита от превышения Gemini Free Tier

## 📊 Лимиты Gemini 2.5 Flash Free Tier

- **10 RPM** (Requests Per Minute) - Запросов в минуту
- **250,000 TPM** (Tokens Per Minute) - Токенов в минуту
- **250 RPD** (Requests Per Day) - Запросов в день

**Что происходит при превышении:**
- API возвращает ошибку `429 RESOURCE_EXHAUSTED`
- Нужно подождать до сброса лимита (1 минута для RPM, 24 часа для RPD)

---

## 🔒 Двухуровневая защита

### Уровень 1: Google AI Studio (Автоматическая)

✅ **Главное правило: НЕ ВКЛЮЧАТЬ BILLING**

Если billing не подключен:
- Google автоматически блокирует запросы при достижении лимитов
- **Вы никогда не заплатите** - просто получите ошибку 429
- Лимиты сбрасываются автоматически (1 мин / 24 часа)

**Как проверить:**
1. Откройте https://aistudio.google.com/app/apikey
2. Убедитесь что billing НЕ включен
3. Мониторьте usage: https://ai.dev/usage

### Уровень 2: Rate Limiter в коде (Наш)

Мы добавили **программную защиту** на стороне сервера:

```go
// adkagent/rate_limiter.go
rateLimiter := NewGeminiFreeTierLimiter() // 10 RPM, 250 RPD
```

**Что делает:**
- Считает запросы к LLM в минуту и в день
- Блокирует запрос ПЕРЕД отправкой в Gemini API
- Возвращает пользователю: "Достигнут лимит запросов к AI"
- Автоматически сбрасывает счетчики

**Преимущества:**
- ✅ Защита ДО отправки запроса (экономит квоту)
- ✅ Понятное сообщение пользователю
- ✅ Логирование всех превышений
- ✅ Автоматический сброс счетчиков

---

## 🧪 Тестирование

### Тест 1: Базовый функционал
```bash
go run /tmp/test_rate_limiter.go
```

Проверяет:
- ✅ Первые 10 запросов проходят
- ✅ 11-й запрос блокируется
- ✅ Сброс счетчика после 60 секунд
- ✅ Дневной лимит
- ✅ Автоматическое ожидание

### Тест 2: Интеграция с агентом

Создайте тестовый скрипт:

```go
package main

import (
    "context"
    "log"
    "os"

    "github.com/egor/ecochatserver/adkagent"
    "github.com/egor/ecochatserver/llm"
)

func main() {
    os.Setenv("LLM_PROVIDER", "gemini")
    os.Setenv("GEMINI_API_KEY", "your-api-key")
    os.Setenv("GEMINI_MODEL", "gemini-2.5-flash")

    storeClient := llm.NewStoreClient()
    agent, _ := adkagent.NewSupportAgentV2(context.Background(), storeClient, false)

    // Делаем 11 запросов подряд
    for i := 1; i <= 11; i++ {
        log.Printf("\n━━━ Запрос %d ━━━", i)
        response, err := agent.ProcessMessage(context.Background(),
            "test-session", "привет", 0)

        if err != nil {
            log.Printf("❌ Ошибка: %v", err)
        } else {
            log.Printf("✅ Ответ: %s", response)
        }
    }
}
```

**Ожидаемый результат:**
- Запросы 1-10: Успешные ответы от Gemini
- Запрос 11: "Извините, достигнут лимит запросов к AI. RPM: 10/10..."

---

## 📈 Мониторинг Usage

### В логах сервера

```
[RATE_LIMITER] ✅ Запрос разрешен: RPM=1/10, RPD=1/250
[RATE_LIMITER] ✅ Запрос разрешен: RPM=2/10, RPD=2/250
...
[RATE_LIMITER] ⚠️ Превышен лимит RPM: 10/10
[AGENT_V2] ⚠️ Rate limit exceeded: RPM=10/10, RPD=10/250
```

### В Google AI Studio

https://ai.dev/usage?tab=rate-limit

Показывает:
- Использование RPM / TPM / RPD
- Графики за период
- Нарушения лимитов

---

## ⚙️ Настройка лимитов

### Использование дефолтных лимитов (рекомендуется)

```go
// Автоматически: 10 RPM, 250 RPD
agent, _ := adkagent.NewSupportAgentV2(ctx, storeClient, false)
```

### Кастомные лимиты

Если хотите установить более строгие лимиты:

```go
// В adkagent/support_agent_v2.go:89
rateLimiter := adkagent.NewRateLimiter(5, 100) // 5 RPM, 100 RPD (более строгие)
```

### Отключение rate limiter

```go
// В adkagent/support_agent_v2.go:89
rateLimiter := nil // Отключить rate limiter (НЕ РЕКОМЕНДУЕТСЯ!)
```

---

## 🚨 Обработка превышения лимитов

### На фронтенде

Когда пользователь получает сообщение о превышении лимита:

```javascript
if (response.includes("достигнут лимит запросов")) {
    // Показать пользователю красивое уведомление
    showNotification({
        type: "warning",
        title: "Лимит запросов исчерпан",
        message: "Пожалуйста, попробуйте через минуту",
        duration: 5000
    });

    // Опционально: автоматический retry через 60 секунд
    setTimeout(() => retryRequest(), 60000);
}
```

### На бэкенде

Rate limiter автоматически возвращает сообщение:

```
"Извините, достигнут лимит запросов к AI. RPM: 10/10, RPD: 15/250.
Пожалуйста, попробуйте позже."
```

Вы можете кастомизировать это сообщение в `support_agent_v2.go:119`

---

## 💡 Рекомендации

### Для разработки

- ✅ Используйте rate limiter даже в dev (поможет отловить баги)
- ✅ Тестируйте с малыми лимитами (2 RPM) для быстрой проверки
- ✅ Мониторьте логи на предмет частых превышений

### Для production

- ✅ **НЕ включайте billing** если хотите остаться в free tier
- ✅ Используйте дефолтные лимиты (10 RPM, 250 RPD)
- ✅ Добавьте alerting на превышение 80% лимита
- ✅ Рассмотрите переход на paid tier если лимитов не хватает

### Paid Tier опции

Если Free Tier не хватает:

**Gemini 2.5 Flash Paid:**
- 1000 RPM (вместо 10)
- 4M TPM (вместо 250K)
- Безлимитный RPD

**Стоимость:**
- $0.10 / 1M input tokens
- $0.40 / 1M output tokens

**Пример расчета:**
- 1000 запросов/день × 500 tokens avg = 500K tokens
- 500K × $0.25/1M = $0.125/день = **$3.75/месяц**

---

## 📝 Checklist для Production

- [ ] Billing НЕ включен в Google AI Studio
- [ ] Rate limiter добавлен в код
- [ ] Тесты rate limiter проходят
- [ ] Логирование работает
- [ ] Фронтенд обрабатывает сообщения о лимитах
- [ ] Мониторинг usage настроен
- [ ] Алерты на превышение лимитов
- [ ] Документация обновлена

---

## 🔗 Полезные ссылки

- **Gemini API Keys**: https://aistudio.google.com/app/apikey
- **Usage Dashboard**: https://ai.dev/usage
- **Rate Limits Docs**: https://ai.google.dev/gemini-api/docs/rate-limits
- **Pricing**: https://ai.google.dev/pricing

---

## ❓ FAQ

**Q: Могу ли я увеличить free tier лимиты?**
A: Нет, free tier лимиты фиксированные. Нужно переходить на paid tier.

**Q: Что если я случайно включил billing?**
A: Отключите billing в Google Cloud Console → не будет списаний.

**Q: Rate limiter сбрасывает счетчики при рестарте сервера?**
A: Да, счетчики в памяти. Для persistence нужно добавить Redis/БД.

**Q: Можно ли установить лимиты на уровне пользователя?**
A: Да, нужно создать `map[userID]*RateLimiter` в агенте.

**Q: Как мониторить реальное использование?**
A: https://ai.dev/usage + логи rate limiter в коде
