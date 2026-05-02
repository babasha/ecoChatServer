-- Поддержка chat-связи client↔driver для проекта moooving
-- moooving использует int32/int64 ID-ки, поэтому храним их как BIGINT
-- без преобразования в UUID. UUID-шки внутри ecoChat остаются как primary keys.

BEGIN;

-- 1. Добавляем колонки для хранения внешних int-ID из moooving
ALTER TABLE chats
    ADD COLUMN IF NOT EXISTS order_id        BIGINT,
    ADD COLUMN IF NOT EXISTS client_id_ext   BIGINT,
    ADD COLUMN IF NOT EXISTS driver_id_ext   BIGINT;

COMMENT ON COLUMN chats.order_id      IS 'ID заказа из moooving (api.OrderRecord.id)';
COMMENT ON COLUMN chats.client_id_ext IS 'ID клиента из moooving (api.UserResponse.id с role=user)';
COMMENT ON COLUMN chats.driver_id_ext IS 'ID водителя из moooving (api.UserResponse.id с role=driver)';

-- 2. Уникальность: один активный (не архивированный) чат на заказ
CREATE UNIQUE INDEX IF NOT EXISTS uq_chats_active_order_id
    ON chats (order_id)
    WHERE order_id IS NOT NULL AND is_archived = false;

-- 3. Индексы для быстрых выборок driver/client панели
CREATE INDEX IF NOT EXISTS idx_chats_driver_id_ext_active
    ON chats (driver_id_ext, updated_at DESC)
    WHERE driver_id_ext IS NOT NULL AND is_archived = false;

CREATE INDEX IF NOT EXISTS idx_chats_client_id_ext_active
    ON chats (client_id_ext, updated_at DESC)
    WHERE client_id_ext IS NOT NULL AND is_archived = false;

-- 4. Поддержка sender='driver' в партиционированной таблице messages
-- Существующие constraints (см. migrations_fixed.sql):
--   unique_user_message_with_timestamp  WHERE sender='user'
--   unique_admin_message_with_timestamp WHERE sender='admin'
-- Добавляем аналог для driver:
CREATE UNIQUE INDEX IF NOT EXISTS unique_driver_message_with_timestamp
    ON messages (chat_id, sender_id, content_hash, timestamp)
    WHERE sender = 'driver';

COMMENT ON INDEX unique_driver_message_with_timestamp IS
    'Предотвращает дублирование сообщений водителя (для партиционированной таблицы)';

-- 5. Push subscriptions: расширяем для moooving (driver/client) пользователей.
-- В существующей таблице push_subscriptions(admin_id) admin_id остаётся для
-- админов ecoChat; для moooving подписок добавляем source ('moooving_driver'|
-- 'moooving_client') + ext_user_id (int из Rust).
ALTER TABLE push_subscriptions
    ADD COLUMN IF NOT EXISTS source       TEXT,
    ADD COLUMN IF NOT EXISTS ext_user_id  BIGINT;

CREATE INDEX IF NOT EXISTS idx_push_subscriptions_moooving
    ON push_subscriptions (source, ext_user_id)
    WHERE source IS NOT NULL;

COMMENT ON COLUMN push_subscriptions.source      IS 'moooving_driver|moooving_client (NULL для admin-подписок)';
COMMENT ON COLUMN push_subscriptions.ext_user_id IS 'int-ID пользователя в moooving';

COMMIT;
