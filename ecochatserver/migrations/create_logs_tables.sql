-- Создание таблиц для системных логов
-- Эти таблицы используются для хранения логов приложения для мониторинга и отладки

-- Таблица серверных логов
CREATE TABLE IF NOT EXISTS server_logs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    timestamp timestamp with time zone NOT NULL DEFAULT now(),
    level varchar(20) NOT NULL,
    message text NOT NULL,
    source varchar(100),
    metadata jsonb,
    created_at timestamp with time zone NOT NULL DEFAULT now()
);

-- Индексы для server_logs
CREATE INDEX IF NOT EXISTS idx_server_logs_timestamp ON server_logs (timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_server_logs_level ON server_logs (level);
CREATE INDEX IF NOT EXISTS idx_server_logs_source ON server_logs (source);
CREATE INDEX IF NOT EXISTS idx_server_logs_created_at ON server_logs (created_at DESC);

-- Таблица WebSocket логов
CREATE TABLE IF NOT EXISTS websocket_logs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    timestamp timestamp with time zone NOT NULL DEFAULT now(),
    level varchar(20) NOT NULL,
    message text NOT NULL,
    source varchar(100),
    client_id uuid,
    client_type varchar(20),
    metadata jsonb,
    created_at timestamp with time zone NOT NULL DEFAULT now()
);

-- Индексы для websocket_logs
CREATE INDEX IF NOT EXISTS idx_websocket_logs_timestamp ON websocket_logs (timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_websocket_logs_level ON websocket_logs (level);
CREATE INDEX IF NOT EXISTS idx_websocket_logs_source ON websocket_logs (source);
CREATE INDEX IF NOT EXISTS idx_websocket_logs_client_id ON websocket_logs (client_id);
CREATE INDEX IF NOT EXISTS idx_websocket_logs_client_type ON websocket_logs (client_type);
CREATE INDEX IF NOT EXISTS idx_websocket_logs_created_at ON websocket_logs (created_at DESC);

-- Функция для очистки старых логов (старше 24 часов)
CREATE OR REPLACE FUNCTION cleanup_old_logs()
RETURNS void AS $$
BEGIN
    DELETE FROM server_logs WHERE created_at < NOW() - INTERVAL '24 hours';
    DELETE FROM websocket_logs WHERE created_at < NOW() - INTERVAL '24 hours';
END;
$$ LANGUAGE plpgsql;

-- Комментарии к таблицам
COMMENT ON TABLE server_logs IS 'Логи работы сервера для мониторинга и отладки';
COMMENT ON TABLE websocket_logs IS 'Логи WebSocket соединений и сообщений';
