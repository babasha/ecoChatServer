-- Миграция данных из таблицы admins в users
-- Дата: 2025-10-12
-- Стратегия: удаляем старые записи с каскадным удалением связей и создаем новые с ID из admins

BEGIN;

-- 1. Удаляем существующие записи админов из users (CASCADE удалит все связи)
DELETE FROM users
WHERE email IN (SELECT email FROM admins);

-- 2. Вставляем записи из admins с их оригинальными ID
INSERT INTO users (
    id,
    email,
    email_verified,
    display_name,
    password_hash,
    role_id,
    status,
    created_at,
    updated_at,
    last_login_at
)
SELECT
    a.id,
    a.email,
    true, -- email уже проверен для админов
    a.name,
    a.password, -- в admins колонка называется password, а в users - password_hash
    r.id as role_id,
    'active',
    a.created_at,
    a.updated_at,
    a.last_login_at
FROM admins a
LEFT JOIN roles r ON r.name = a.role;

-- 3. Проверяем результат миграции
SELECT
    u.id as user_id,
    u.email,
    u.display_name,
    r.name as role_name,
    u.status,
    u.password_hash IS NOT NULL as has_password
FROM users u
LEFT JOIN roles r ON r.id = u.role_id
WHERE u.email IN (
    SELECT email FROM admins
)
ORDER BY u.email;

COMMIT;

-- После успешной миграции и тестирования выполнить:
-- DROP TABLE admins CASCADE;
