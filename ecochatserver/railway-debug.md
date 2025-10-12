# Railway Private Networking Debug Guide

## Проблема
Ошибка: `lookup chatdb.railway.internal: no such host`

## Причина
Railway не может найти DNS-имя `chatdb.railway.internal`. Это означает, что либо:
1. Сервис БД называется НЕ "chatdb" в Railway
2. Private Networking не включен для этого сервиса
3. Формат hostname неверный

## Решение

### Шаг 1: Найти правильные имена сервисов

В Railway Dashboard:
1. Откройте ваш проект
2. Для КАЖДОЙ базы данных (3 штуки):
   - Откройте сервис БД
   - Найдите раздел **"Settings" → "Service Name"**
   - Скопируйте ТОЧНОЕ имя (например, может быть "Postgres", "postgres-chatdb", "database-1" и т.д.)

### Шаг 2: Проверить Private Networking

Для каждого сервиса БД:
1. Откройте вкладку **"Networking"**
2. Убедитесь что **"Private Networking"** включен (зеленая галочка "Ready to talk privately")
3. Скопируйте точный адрес из поля **"Private Domain"** (формат: `ServiceName.railway.internal`)

### Шаг 3: Обновить переменные окружения

Используйте ТОЧНЫЕ значения из "Private Domain":

#### Для БД чатов (caboose):
```
PG_HOST=<точное-имя-из-private-domain>    # например: postgres-production-abc123.railway.internal
PG_PORT=5432                               # НЕ 31654! Внутри приватной сети порт всегда 5432
PG_USER=postgres
PG_PASSWORD=<пароль из caboose>
PG_DATABASE=railway
PG_SSL_MODE=require                        # Для приватной сети используйте require
```

#### Для БД юзеров (ballast):
```
USERS_PG_HOST=<точное-имя-из-private-domain>
USERS_PG_PORT=5432                         # НЕ 58306!
USERS_PG_USER=postgres
USERS_PG_PASSWORD=<пароль из ballast>
USERS_PG_DATABASE=railway
USERS_PG_SSL_MODE=require
```

#### Для БД LLM логов (shuttle):
```
LLM_PG_HOST=<точное-имя-из-private-domain>
LLM_PG_PORT=5432                           # НЕ 23652!
LLM_PG_USER=postgres
LLM_PG_PASSWORD=<пароль из shuttle>
LLM_PG_DATABASE=railway
LLM_PG_SSL_MODE=require
```

### Шаг 4: Альтернативный метод (Railway Variable References)

Вместо ручного копирования, можно использовать автоматические ссылки Railway:

```
PG_HOST=${{Postgres.RAILWAY_PRIVATE_DOMAIN}}
PG_PORT=5432
PG_USER=${{Postgres.PGUSER}}
PG_PASSWORD=${{Postgres.PGPASSWORD}}
PG_DATABASE=${{Postgres.PGDATABASE}}
```

Где `Postgres` - это ТОЧНОЕ имя сервиса БД в Railway.

### Важные моменты:

1. **Порты**: В приватной сети Railway ВСЕГДА используется порт `5432` для PostgreSQL, независимо от внешнего порта (31654, 58306, 23652)

2. **SSL Mode**: Для приватной сети используйте `require` (не `disable`)

3. **Имена сервисов**: Они могут быть любыми, например:
   - "Postgres"
   - "chatdb-prod"
   - "database-users"
   - "postgres-production-a1b2c3"

4. **Формат hostname**: `<service-name>.railway.internal` (Railway добавляет `.railway.internal` автоматически)

## Как проверить правильность настройки

После обновления переменных:
1. Railway автоматически перезапустит сервис
2. В логах должно появиться:
   ```
   [database] PostgreSQL (chats) connected ✓
   [database] PostgreSQL (users) connected ✓
   ```
3. Ошибки "no such host" должны исчезнуть

## Что сделать прямо сейчас

1. Откройте Railway Dashboard
2. Найдите 3 сервиса PostgreSQL (caboose, ballast, shuttle)
3. Для каждого скопируйте:
   - Service Name (из Settings)
   - Private Domain (из Networking)
   - Password (из Variables или Connect)
4. Пришлите мне эти значения, и я обновлю конфигурацию
