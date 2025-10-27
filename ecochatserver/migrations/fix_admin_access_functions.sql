-- Упрощение функций для работы без таблицы roles в main DB
-- Роли админов хранятся в auth DB, поэтому логику перенесли в Go код

-- Удаляем старую функцию
DROP FUNCTION IF EXISTS get_admins_with_chat_access(uuid);

-- Новая упрощенная функция: возвращает только admin_id
-- Go код сам определит роли из auth DB
CREATE OR REPLACE FUNCTION get_admins_with_chat_access_simple(p_chat_id uuid)
RETURNS TABLE(admin_id uuid) AS $$
BEGIN
    RETURN QUERY
    WITH chat_info AS (
        SELECT c.client_id, c.source, u.source_id, c.assigned_to
        FROM chats c
        LEFT JOIN users u ON c.user_id = u.id
        WHERE c.id = p_chat_id
    )
    -- Админы с доступом к источнику (supervisors)
    SELECT DISTINCT asa.admin_id
    FROM admin_source_access asa
    JOIN chat_info ci ON asa.client_id = ci.client_id
        AND asa.source_type = ci.source
        AND (asa.source_id IS NULL OR asa.source_id = ci.source_id)

    UNION

    -- Supervisors, чьим менеджерам назначен чат
    SELECT DISTINCT ah.supervisor_id
    FROM admin_hierarchy ah
    JOIN chat_info ci ON ah.manager_id = ci.assigned_to
        AND ah.client_id = ci.client_id

    UNION

    -- Manager, которому назначен чат
    SELECT ci.assigned_to
    FROM chat_info ci
    WHERE ci.assigned_to IS NOT NULL;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION get_admins_with_chat_access_simple IS 'Возвращает ID админов с доступом к чату (без определения ролей)';
