# Instagram Direct Messages Integration

## Переменные окружения

Для работы Instagram интеграции необходимо настроить следующие переменные окружения:

### Обязательные переменные:

```bash
# Токен для верификации webhook (генерируется произвольно)
INSTAGRAM_VERIFY_TOKEN=your_random_verify_token

# ID Instagram Business аккаунта
INSTAGRAM_BUSINESS_ACCOUNT_ID=your_business_account_id

# Секрет Facebook приложения (из Developer Console)
INSTAGRAM_APP_SECRET=your_app_secret

# Долгосрочный токен доступа Instagram
INSTAGRAM_ACCESS_TOKEN=your_long_lived_access_token

# Версия Instagram Graph API (рекомендуется v21.0 или выше)
INSTAGRAM_API_VERSION=v21.0

# Ключ клиента для внутренней идентификации
INSTAGRAM_CLIENT_API_KEY=instagram_default_client
```

## Настройка в Facebook Developer Console

1. Создайте приложение Facebook
2. Добавьте продукт "Instagram"
3. Настройте webhook:
   - URL: `https://your-domain.com/api/instagram/webhook`
   - Verify Token: значение из `INSTAGRAM_VERIFY_TOKEN`
   - Подпишитесь на поле `messages`

4. Получите необходимые разрешения:
   - `instagram_basic`
   - `instagram_manage_messages`
   - `business_management`
   - `pages_read_engagement`

## База данных

Убедитесь, что в ENUM `chat_source` есть значение `instagram`:

```sql
ALTER TYPE chat_source ADD VALUE IF NOT EXISTS 'instagram';
ALTER TYPE chat_source ADD VALUE IF NOT EXISTS 'telegram';
```

## Endpoints

- `GET /api/instagram/webhook` - Верификация webhook
- `POST /api/instagram/webhook` - Получение сообщений от Instagram

## Тестирование

После настройки отправьте Direct Message на ваш Instagram Business аккаунт. Сообщение должно:
1. Быть получено через webhook
2. Сохранено в базу данных
3. Отображено в админ-панели

## Troubleshooting

### Ошибка: "invalid input value for enum chat_source: instagram"
Выполните SQL миграцию для добавления значения в ENUM (см. раздел "База данных")

### Webhook не получает сообщения
1. Проверьте, что приложение опубликовано или тестовый пользователь добавлен
2. Проверьте логи Railway на предмет ошибок
3. Убедитесь, что webhook URL доступен и возвращает challenge при верификации

### Сообщения не сохраняются
Проверьте логи на наличие ошибок партиционирования таблицы `messages`
