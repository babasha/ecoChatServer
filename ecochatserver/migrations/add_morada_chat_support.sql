-- Поддержка chat-связи visitor↔agent для проекта morada (недвижимость).
-- morada — это «посетитель сайта общается с владельцем/агентством об объекте».
-- Структурно это тот же двусторонний внешний чат, что и moooving, поэтому
-- ПЕРЕИСПОЛЬЗУЕМ существующие колонки:
--   chats.client_id_ext  → ID посетителя в morada (int)
--   chats.driver_id_ext  → ID владельца/агента в morada (int, NULL если не назначен)
-- и сообщения посетителя пишутся с sender='user', агента — sender='driver'
-- (индексы дедупликации unique_user_/unique_driver_ уже есть).
-- Добавляем только привязку к объекту недвижимости.

BEGIN;

-- 1. Листинг (объект), о котором идёт чат.
ALTER TABLE chats
    ADD COLUMN IF NOT EXISTS morada_listing_id BIGINT;

COMMENT ON COLUMN chats.morada_listing_id IS 'ID объекта недвижимости в morada (listings.id), о котором идёт чат';

-- 2. Один активный (не архивированный) чат на пару (объект, посетитель).
CREATE UNIQUE INDEX IF NOT EXISTS uq_chats_active_morada_listing_visitor
    ON chats (morada_listing_id, client_id_ext)
    WHERE source = 'morada' AND is_archived = false;

-- 3. Быстрая выборка чатов по объекту (например, все обращения по листингу).
CREATE INDEX IF NOT EXISTS idx_chats_morada_listing_active
    ON chats (morada_listing_id, updated_at DESC)
    WHERE morada_listing_id IS NOT NULL AND is_archived = false;

COMMIT;
