-- Чат «посетитель сайта ↔ поддержка сайта» для morada/tudonuma.
-- Тот же morada-чат (source='morada'), но без объекта и без агента: отвечает
-- команда поддержки (любой админ сайта). См. database/queries/chat_morada_support.go.
--
-- Сервер применяет то же самое при старте (EnsureMoradaSupportSchema), так что
-- ручной прогон нужен только для базы, которую сервер ещё не видел.

BEGIN;

ALTER TABLE chats ADD COLUMN IF NOT EXISTS morada_listing_id BIGINT;
ALTER TABLE chats ADD COLUMN IF NOT EXISTS morada_support BOOLEAN NOT NULL DEFAULT false;

COMMENT ON COLUMN chats.morada_support IS 'true — чат посетителя morada/tudonuma с поддержкой сайта (без объекта и агента)';

-- Один активный чат поддержки на посетителя.
CREATE UNIQUE INDEX IF NOT EXISTS uq_chats_active_morada_support_visitor
    ON chats (client_id_ext)
    WHERE source = 'morada' AND morada_support AND is_archived = false;

-- Инбокс поддержки: по последней активности.
CREATE INDEX IF NOT EXISTS idx_chats_morada_support_updated
    ON chats (updated_at DESC)
    WHERE source = 'morada' AND morada_support AND is_archived = false;

COMMIT;
