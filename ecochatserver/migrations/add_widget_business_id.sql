-- Добавление поля widget_business_id в таблицу chats
-- Это поле хранит идентификатор бизнеса/виджета (например, "enddel")
-- Используется для привязки supervisor'ов к конкретным виджетам

-- Добавляем колонку widget_business_id
ALTER TABLE chats
ADD COLUMN IF NOT EXISTS widget_business_id VARCHAR(100);

-- Создаем индекс для быстрого поиска чатов по widget_business_id
CREATE INDEX IF NOT EXISTS idx_chats_widget_business_id
ON chats (widget_business_id);

-- Создаем составной индекс для фильтрации по клиенту и виджету
CREATE INDEX IF NOT EXISTS idx_chats_client_widget
ON chats (client_id, widget_business_id);

-- Комментарий к полю
COMMENT ON COLUMN chats.widget_business_id IS 'Идентификатор бизнеса/виджета (например, "enddel"). Используется для привязки supervisor к конкретным виджетам.';

-- Обновление существующих записей: для web_widget чатов без widget_business_id
-- устанавливаем значение 'default' для обратной совместимости
UPDATE chats
SET widget_business_id = 'default'
WHERE source = 'web_widget'
AND widget_business_id IS NULL;
