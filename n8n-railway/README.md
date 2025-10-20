# n8n на Railway для Instagram Integration

Развертывание n8n для пересылки Instagram webhooks на ваш бэкенд.

## 🚀 Быстрый старт (Рекомендуется)

### Вариант 1: Через Railway Template (САМЫЙ ПРОСТОЙ)

1. Перейдите по ссылке: **https://railway.app/template/n8n**
2. Нажмите **"Deploy Now"**
3. Дождитесь завершения развертывания (2-3 минуты)
4. Получите URL вашего n8n (например: `https://n8n-production.up.railway.app`)

Railway автоматически:
- ✅ Создаст PostgreSQL базу для n8n
- ✅ Настроит все переменные окружения
- ✅ Выдаст публичный HTTPS URL

### Вариант 2: Вручную через Railway Dashboard

1. Зайдите на https://railway.app/dashboard
2. Нажмите **"New Project"**
3. Выберите **"Deploy from Docker Image"**
4. Введите: `n8nio/n8n:latest`
5. Добавьте переменные окружения:
   ```
   N8N_PORT=5678
   N8N_PROTOCOL=https
   WEBHOOK_URL=https://your-generated-url.railway.app/
   ```
6. Добавьте PostgreSQL:
   - Нажмите **"+ New"** → **"Database"** → **"Add PostgreSQL"**
   - Railway автоматически свяжет базу с n8n

## 📋 После развертывания

### 1. Первый вход в n8n

1. Откройте URL вашего n8n (показан в Railway Dashboard)
2. Создайте первого пользователя (admin):
   - Email: ваш email
   - Password: надежный пароль

### 2. Настройка Instagram Webhook в n8n

#### Шаг 2.1: Создать Workflow

1. В n8n нажмите **"+ Add workflow"**
2. Назовите: **"Instagram DM Forwarder"**

#### Шаг 2.2: Добавить Webhook Trigger

1. Нажмите **"+ Add first step"**
2. Найдите и выберите **"Webhook"**
3. Настройки:
   - **HTTP Method**: POST
   - **Path**: `instagram-webhook`
   - **Response Mode**: "Respond Immediately"
   - **Response Code**: 200

Сохраните. n8n создаст URL:
```
https://your-n8n.railway.app/webhook/instagram-webhook
```

#### Шаг 2.3: Добавить HTTP Request

1. Нажмите **"+"** после Webhook node
2. Выберите **"HTTP Request"**
3. Настройки:
   - **Method**: POST
   - **URL**: `https://ecochatserver-production.up.railway.app/api/instagram/webhook`
   - **Body**: JSON
   - **JSON/RAW Parameters**: включить
   - В поле JSON введите:
     ```json
     {{ $json }}
     ```

#### Шаг 2.4: Добавить Headers (опционально)

В HTTP Request node:
- Добавьте Header:
  - **Name**: `X-Forwarded-From`
  - **Value**: `n8n-instagram-forwarder`

#### Шаг 2.5: Активировать Workflow

1. Нажмите **"Active"** (переключатель в правом верхнем углу)
2. Workflow готов принимать webhooks!

### 3. Подключить n8n к Instagram

Теперь нужно настроить Facebook App, чтобы он отправлял webhooks на n8n:

#### Вариант A: Использовать ManyChat (проще)

1. Зайдите на https://manychat.com
2. Подключите свой Instagram аккаунт
3. В настройках **External Request** укажите:
   ```
   https://your-n8n.railway.app/webhook/instagram-webhook
   ```

#### Вариант B: Прямая интеграция (требует n8n credentials)

1. В n8n перейдите **Credentials** → **+ Add Credential**
2. Найдите **"Instagram"** или **"Facebook"**
3. Добавьте:
   - **App ID**: `851510793964903`
   - **App Secret**: `4245ce4c879f7251c7737abd506111df`
   - **Access Token**: `IGAAksn0eLh1VBZAFRNeHZAGM2F3TDRVU1djZA1lmWnoxWU5FUmd4RVdtX0ZAzbVJXMk9tWWhYSVItbjZArUkVZAVXNBZAGY4RFBxQ1FaYks5Um5CVW9DZAVd4U3ZAsMHBYZAzlDcktJQVdHdW55MlF6WnFlUm9LMkRxUFg1cDBkOFh0UmNjTQZDZD`

4. Используйте эти credentials в workflow

## 🧪 Тестирование

### Тест 1: Проверка Webhook

```bash
curl -X POST https://your-n8n.railway.app/webhook/instagram-webhook \
  -H "Content-Type: application/json" \
  -d '{
    "object": "instagram",
    "entry": [{
      "id": "17841400772641672",
      "time": 1729387200,
      "messaging": [{
        "sender": {"id": "17841405432437945", "username": "dverki64"},
        "recipient": {"id": "17841400772641672"},
        "timestamp": "1729387200",
        "message": {"mid": "test_123", "text": "Тест через n8n"}
      }]
    }]
  }'
```

### Тест 2: Проверка в n8n

1. Зайдите в n8n → ваш workflow
2. Нажмите **"Execute Workflow"**
3. Посмотрите логи выполнения

### Тест 3: Проверка на бэкенде

Проверьте логи Railway ecochatserver - должно появиться сообщение.

## 🔧 Переменные окружения Railway

В Railway для n8n сервиса должны быть настроены:

```env
N8N_PORT=5678
N8N_PROTOCOL=https
WEBHOOK_URL=https://your-n8n.railway.app/
DB_TYPE=postgresdb
DB_POSTGRESDB_DATABASE=${{Postgres.PGDATABASE}}
DB_POSTGRESDB_HOST=${{Postgres.PGHOST}}
DB_POSTGRESDB_PORT=${{Postgres.PGPORT}}
DB_POSTGRESDB_USER=${{Postgres.PGUSER}}
DB_POSTGRESDB_PASSWORD=${{Postgres.PGPASSWORD}}
```

Railway автоматически подставит значения PostgreSQL при использовании template.

## 📊 Мониторинг

### Логи n8n

В Railway Dashboard:
1. Выберите n8n service
2. Перейдите в **"Logs"**
3. Смотрите в реальном времени

### Executions в n8n

В n8n:
1. Перейдите в **"Executions"** (левое меню)
2. Смотрите историю всех выполнений workflow
3. Кликните на execution для деталей

## 🐛 Troubleshooting

### Webhook не приходит

1. Проверьте, активен ли workflow (переключатель "Active")
2. Проверьте URL webhook в Facebook App
3. Проверьте логи Railway

### Ошибка пересылки на backend

1. Проверьте URL в HTTP Request node
2. Проверьте, работает ли ecochatserver
3. Посмотрите логи execution в n8n

### n8n не запускается

1. Проверьте подключение к PostgreSQL
2. Проверьте переменные окружения
3. Перезапустите service в Railway

## 💰 Стоимость

Railway Free Tier:
- $5 кредитов/месяц бесплатно
- n8n + PostgreSQL ≈ $3-5/месяц
- Должно хватить для тестирования

## 🔐 Безопасность

Рекомендуется:
1. Добавить Basic Auth в n8n (Settings → Security)
2. Использовать webhook signature verification
3. Ограничить доступ к n8n URL

## 📚 Дополнительно

- [n8n Documentation](https://docs.n8n.io)
- [n8n Instagram Templates](https://n8n.io/workflows/?integrations=instagram)
- [Railway Documentation](https://docs.railway.app)
