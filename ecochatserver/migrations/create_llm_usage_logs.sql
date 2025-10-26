-- Создание таблицы для логирования использования LLM токенов
-- Используется для мониторинга расходов на AI и аналитики

CREATE TABLE IF NOT EXISTS llm_usage_logs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    timestamp timestamp with time zone NOT NULL DEFAULT now(),
    client_id uuid,
    chat_id uuid,
    admin_id uuid,
    provider varchar(50) NOT NULL,  -- "openai", "anthropic", "local-model", etc.
    model varchar(100) NOT NULL,     -- "gpt-4", "claude-3-opus", etc.
    prompt_tokens integer NOT NULL DEFAULT 0,
    completion_tokens integer NOT NULL DEFAULT 0,
    total_tokens integer NOT NULL DEFAULT 0,
    request_type varchar(50) NOT NULL,  -- "chat", "translation", "autoresponder", "analysis", "function_call"
    metadata jsonb,
    created_at timestamp with time zone NOT NULL DEFAULT now()
);

-- Индексы для быстрого поиска и аналитики
CREATE INDEX IF NOT EXISTS idx_llm_usage_logs_timestamp ON llm_usage_logs (timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_llm_usage_logs_client_id ON llm_usage_logs (client_id);
CREATE INDEX IF NOT EXISTS idx_llm_usage_logs_chat_id ON llm_usage_logs (chat_id);
CREATE INDEX IF NOT EXISTS idx_llm_usage_logs_admin_id ON llm_usage_logs (admin_id);
CREATE INDEX IF NOT EXISTS idx_llm_usage_logs_provider ON llm_usage_logs (provider);
CREATE INDEX IF NOT EXISTS idx_llm_usage_logs_model ON llm_usage_logs (model);
CREATE INDEX IF NOT EXISTS idx_llm_usage_logs_request_type ON llm_usage_logs (request_type);
CREATE INDEX IF NOT EXISTS idx_llm_usage_logs_created_at ON llm_usage_logs (created_at DESC);

-- Функция для очистки старых логов (старше 90 дней)
CREATE OR REPLACE FUNCTION cleanup_old_llm_usage_logs()
RETURNS void AS $$
BEGIN
    DELETE FROM llm_usage_logs WHERE created_at < NOW() - INTERVAL '90 days';
END;
$$ LANGUAGE plpgsql;

-- Комментарии к таблице
COMMENT ON TABLE llm_usage_logs IS 'Логи использования LLM для мониторинга расходов и аналитики';
COMMENT ON COLUMN llm_usage_logs.client_id IS 'ID клиента для которого выполнялся запрос';
COMMENT ON COLUMN llm_usage_logs.chat_id IS 'ID чата в котором выполнялся запрос';
COMMENT ON COLUMN llm_usage_logs.admin_id IS 'ID админа который инициировал запрос (для переводов)';
COMMENT ON COLUMN llm_usage_logs.provider IS 'Провайдер LLM (openai, anthropic, local-model)';
COMMENT ON COLUMN llm_usage_logs.model IS 'Название модели';
COMMENT ON COLUMN llm_usage_logs.request_type IS 'Тип запроса (chat, translation, autoresponder, analysis)';
COMMENT ON COLUMN llm_usage_logs.metadata IS 'Дополнительные метаданные запроса';
