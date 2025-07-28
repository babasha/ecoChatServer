-- Исправленная миграция для партиционированной таблицы messages

-- 1. Колонки content_hash и created_minute уже добавлены, проверяем их заполнение
UPDATE messages 
SET 
    content_hash = encode(digest(content, 'sha256'), 'hex'),
    created_minute = DATE_TRUNC('minute', timestamp)
WHERE content_hash IS NULL OR created_minute IS NULL;

-- 2. Для партиционированной таблицы создаем unique constraints с включением timestamp
-- Создаем unique constraint, который включает timestamp (требование PostgreSQL для партиций)
CREATE UNIQUE INDEX IF NOT EXISTS unique_user_message_with_timestamp 
ON messages (chat_id, sender_id, content_hash, timestamp)
WHERE sender = 'user';

-- 3. Separate constraint для admin сообщений
CREATE UNIQUE INDEX IF NOT EXISTS unique_admin_message_with_timestamp 
ON messages (chat_id, sender_id, content_hash, timestamp)
WHERE sender = 'admin';

-- 4. Создаем улучшенную функцию для безопасной вставки сообщений
CREATE OR REPLACE FUNCTION insert_message_safe(
    p_id UUID,
    p_chat_id UUID,
    p_content TEXT,
    p_sender VARCHAR(50),
    p_sender_id UUID,
    p_timestamp TIMESTAMPTZ,
    p_type VARCHAR(50) DEFAULT 'text',
    p_metadata JSONB DEFAULT '{}'::jsonb
) RETURNS UUID AS $$
DECLARE
    v_message_id UUID;
    v_content_hash VARCHAR(64);
    v_created_minute TIMESTAMP;
BEGIN
    -- Вычисляем хэш контента и округленное время
    v_content_hash := encode(digest(p_content, 'sha256'), 'hex');
    v_created_minute := DATE_TRUNC('minute', p_timestamp);
    
    -- Пытаемся найти существующее сообщение с теми же параметрами
    SELECT id INTO v_message_id
    FROM messages 
    WHERE chat_id = p_chat_id 
      AND sender_id = p_sender_id 
      AND content_hash = v_content_hash
      AND timestamp = p_timestamp
      AND sender = p_sender
    LIMIT 1;
    
    -- Если найдено, возвращаем существующий ID
    IF v_message_id IS NOT NULL THEN
        RETURN v_message_id;
    END IF;
    
    -- Если не найдено, вставляем новое сообщение
    INSERT INTO messages (
        id, chat_id, content, sender, sender_id, 
        timestamp, type, metadata, read, content_hash, created_minute
    ) VALUES (
        p_id, p_chat_id, p_content, p_sender, p_sender_id,
        p_timestamp, p_type, p_metadata, false, v_content_hash, v_created_minute
    );
    
    RETURN p_id;
    
EXCEPTION
    WHEN unique_violation THEN
        -- В случае race condition, пытаемся найти существующее сообщение еще раз
        SELECT id INTO v_message_id
        FROM messages 
        WHERE chat_id = p_chat_id 
          AND sender_id = p_sender_id 
          AND content_hash = v_content_hash
          AND timestamp = p_timestamp
          AND sender = p_sender
        LIMIT 1;
        
        RETURN COALESCE(v_message_id, p_id);
END;
$$ LANGUAGE plpgsql;

-- 5. Создаем индексы для оптимизации поиска
CREATE INDEX IF NOT EXISTS idx_messages_content_hash_lookup
ON messages (chat_id, sender_id, content_hash, timestamp);

CREATE INDEX IF NOT EXISTS idx_messages_chat_timestamp_perf 
ON messages (chat_id, timestamp DESC);

-- 6. Добавляем constraint для валидации временных меток (исправляем синтаксис)
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.check_constraints 
        WHERE constraint_name = 'check_message_timestamp_reasonable'
    ) THEN
        ALTER TABLE messages 
        ADD CONSTRAINT check_message_timestamp_reasonable
        CHECK (
            timestamp >= '2020-01-01'::timestamptz 
            AND timestamp <= (NOW() + INTERVAL '1 hour')
        );
    END IF;
END $$;

-- Комментарии для документации
COMMENT ON COLUMN messages.content_hash IS 'SHA-256 хэш контента сообщения для дедупликации';
COMMENT ON COLUMN messages.created_minute IS 'Время создания сообщения, округленное до минуты';
COMMENT ON FUNCTION insert_message_safe IS 'Безопасная вставка сообщения с автоматической дедупликацией для партиционированных таблиц';
COMMENT ON INDEX unique_user_message_with_timestamp IS 'Предотвращает дублирование пользовательских сообщений (с учетом партиционирования)';
COMMENT ON INDEX unique_admin_message_with_timestamp IS 'Предотвращает дублирование админских сообщений (с учетом партиционирования)';

-- Проверяем результат
SELECT 
    'migration_completed' as status,
    COUNT(*) as total_messages,
    COUNT(CASE WHEN content_hash IS NOT NULL THEN 1 END) as messages_with_hash,
    COUNT(CASE WHEN created_minute IS NOT NULL THEN 1 END) as messages_with_minute
FROM messages;