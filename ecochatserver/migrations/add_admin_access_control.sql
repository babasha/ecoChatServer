-- Миграция для системы контроля доступа админов к чатам
-- Поддерживает иерархию: Super Admin -> Supervisor -> Manager

-- ============================================================================
-- 1. Таблица для привязки supervisors к источникам чатов
-- ============================================================================
CREATE TABLE IF NOT EXISTS admin_source_access (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_id uuid NOT NULL,  -- ID супервайзера (из users таблицы в auth DB)
    client_id uuid NOT NULL, -- ID клиента (widget owner)
    source_type varchar(50) NOT NULL, -- 'web_widget', 'telegram', 'instagram', 'whatsapp'
    source_id text,          -- Конкретный ID источника (optional, NULL = все источники этого типа)
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now(),

    -- Уникальность: один админ не может иметь дубликаты доступа
    UNIQUE(admin_id, client_id, source_type, source_id)
);

-- Индексы для быстрого поиска
CREATE INDEX IF NOT EXISTS idx_admin_source_access_admin_id ON admin_source_access (admin_id);
CREATE INDEX IF NOT EXISTS idx_admin_source_access_client_id ON admin_source_access (client_id);
CREATE INDEX IF NOT EXISTS idx_admin_source_access_source ON admin_source_access (source_type, source_id);

-- ============================================================================
-- 2. Таблица для иерархии админов (supervisor -> manager)
-- ============================================================================
CREATE TABLE IF NOT EXISTS admin_hierarchy (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    supervisor_id uuid NOT NULL, -- ID супервайзера
    manager_id uuid NOT NULL,    -- ID менеджера под супервайзером
    client_id uuid NOT NULL,     -- ID клиента
    created_at timestamp with time zone NOT NULL DEFAULT now(),

    -- Уникальность: менеджер может быть только под одним супервайзером в рамках клиента
    UNIQUE(manager_id, client_id)
);

-- Индексы для быстрого поиска
CREATE INDEX IF NOT EXISTS idx_admin_hierarchy_supervisor ON admin_hierarchy (supervisor_id);
CREATE INDEX IF NOT EXISTS idx_admin_hierarchy_manager ON admin_hierarchy (manager_id);
CREATE INDEX IF NOT EXISTS idx_admin_hierarchy_client ON admin_hierarchy (client_id);

-- ============================================================================
-- 3. Функция для проверки доступа админа к чату
-- ============================================================================
CREATE OR REPLACE FUNCTION check_admin_access_to_chat(
    p_admin_id uuid,
    p_admin_role text,
    p_chat_id uuid
) RETURNS boolean AS $$
DECLARE
    v_chat_client_id uuid;
    v_chat_source text;
    v_chat_source_id text;
    v_chat_assigned_to uuid;
    v_has_access boolean;
BEGIN
    -- Получаем информацию о чате
    SELECT c.client_id, c.source, u.source_id, c.assigned_to
    INTO v_chat_client_id, v_chat_source, v_chat_source_id, v_chat_assigned_to
    FROM chats c
    LEFT JOIN users u ON c.user_id = u.id
    WHERE c.id = p_chat_id;

    IF NOT FOUND THEN
        RETURN false;
    END IF;

    -- Super Admin видит все
    IF p_admin_role = 'super_admin' THEN
        RETURN true;
    END IF;

    -- Manager видит только свои назначенные чаты
    IF p_admin_role = 'manager' THEN
        RETURN (v_chat_assigned_to = p_admin_id);
    END IF;

    -- Supervisor видит:
    -- 1. Чаты из источников, к которым у него есть доступ
    -- 2. Чаты, назначенные его менеджерам
    IF p_admin_role = 'supervisor' THEN
        -- Проверяем доступ к источнику
        SELECT EXISTS(
            SELECT 1 FROM admin_source_access asa
            WHERE asa.admin_id = p_admin_id
            AND asa.client_id = v_chat_client_id
            AND asa.source_type = v_chat_source
            AND (asa.source_id IS NULL OR asa.source_id = v_chat_source_id)
        ) INTO v_has_access;

        IF v_has_access THEN
            RETURN true;
        END IF;

        -- Проверяем, не назначен ли чат его менеджеру
        SELECT EXISTS(
            SELECT 1 FROM admin_hierarchy ah
            WHERE ah.supervisor_id = p_admin_id
            AND ah.manager_id = v_chat_assigned_to
            AND ah.client_id = v_chat_client_id
        ) INTO v_has_access;

        RETURN v_has_access;
    END IF;

    -- По умолчанию нет доступа
    RETURN false;
END;
$$ LANGUAGE plpgsql;

-- ============================================================================
-- 4. Функция для получения всех админов с доступом к чату
-- ============================================================================
CREATE OR REPLACE FUNCTION get_admins_with_chat_access(p_chat_id uuid)
RETURNS TABLE(admin_id uuid, admin_role text) AS $$
BEGIN
    RETURN QUERY
    WITH chat_info AS (
        SELECT c.client_id, c.source, u.source_id, c.assigned_to
        FROM chats c
        LEFT JOIN users u ON c.user_id = u.id
        WHERE c.id = p_chat_id
    )
    -- Super admins (видят все чаты)
    SELECT DISTINCT u.id, 'super_admin'::text
    FROM users u
    JOIN roles r ON u.role_id = r.id
    WHERE r.name = 'super_admin'

    UNION

    -- Supervisors с доступом к источнику чата
    SELECT DISTINCT asa.admin_id, 'supervisor'::text
    FROM admin_source_access asa
    JOIN chat_info ci ON asa.client_id = ci.client_id
        AND asa.source_type = ci.source
        AND (asa.source_id IS NULL OR asa.source_id = ci.source_id)

    UNION

    -- Supervisors, чьим менеджерам назначен чат
    SELECT DISTINCT ah.supervisor_id, 'supervisor'::text
    FROM admin_hierarchy ah
    JOIN chat_info ci ON ah.manager_id = ci.assigned_to
        AND ah.client_id = ci.client_id

    UNION

    -- Manager, которому назначен чат
    SELECT ci.assigned_to, 'manager'::text
    FROM chat_info ci
    WHERE ci.assigned_to IS NOT NULL;
END;
$$ LANGUAGE plpgsql;

-- ============================================================================
-- Комментарии
-- ============================================================================
COMMENT ON TABLE admin_source_access IS 'Привязка supervisors к источникам чатов (widget, instagram, etc)';
COMMENT ON TABLE admin_hierarchy IS 'Иерархия админов: supervisor управляет managers';
COMMENT ON FUNCTION check_admin_access_to_chat IS 'Проверяет имеет ли админ доступ к чату';
COMMENT ON FUNCTION get_admins_with_chat_access IS 'Возвращает всех админов с доступом к чату';

-- ============================================================================
-- Примеры использования
-- ============================================================================

-- Пример 1: Дать supervisor доступ ко всем Instagram чатам клиента
-- INSERT INTO admin_source_access (admin_id, client_id, source_type, source_id)
-- VALUES ('supervisor-uuid', 'client-uuid', 'instagram', NULL);

-- Пример 2: Дать supervisor доступ к конкретному Instagram аккаунту
-- INSERT INTO admin_source_access (admin_id, client_id, source_type, source_id)
-- VALUES ('supervisor-uuid', 'client-uuid', 'instagram', '17841400772641672');

-- Пример 3: Добавить менеджера под супервайзера
-- INSERT INTO admin_hierarchy (supervisor_id, manager_id, client_id)
-- VALUES ('supervisor-uuid', 'manager-uuid', 'client-uuid');

-- Пример 4: Проверить доступ админа к чату
-- SELECT check_admin_access_to_chat('admin-uuid', 'supervisor', 'chat-uuid');

-- Пример 5: Получить всех админов с доступом к чату
-- SELECT * FROM get_admins_with_chat_access('chat-uuid');
