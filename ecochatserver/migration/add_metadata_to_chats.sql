-- Добавляем поле metadata в таблицу chats
ALTER TABLE chats ADD COLUMN metadata TEXT;

-- Обновляем индексы для поддержки поиска в метаданных (если нужно)
CREATE INDEX IF NOT EXISTS idx_chats_metadata ON chats((json_extract(metadata, '$.autoResponderEnabled')));
