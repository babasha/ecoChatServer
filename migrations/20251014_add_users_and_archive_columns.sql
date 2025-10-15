-- Maintain parity with production schema after HTTP-only session rollout

-- 1. Ensure the lightweight users table exists in the chats database
CREATE TABLE IF NOT EXISTS users (
    id         UUID PRIMARY KEY,
    name       TEXT NOT NULL,
    email      TEXT,
    avatar     TEXT,
    source     TEXT,
    source_id  TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_source_source_id
    ON users (source, source_id);

-- 2. Align archived_chats with server expectations
ALTER TABLE archived_chats
    ADD COLUMN IF NOT EXISTS user_id UUID,
    ADD COLUMN IF NOT EXISTS messages JSONB NOT NULL DEFAULT '[]'::JSONB,
    ADD COLUMN IF NOT EXISTS delete_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS delete_timer_paused BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_archived_chats_delete_at
    ON archived_chats (delete_at)
    WHERE delete_timer_paused = FALSE;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM information_schema.table_constraints
        WHERE constraint_name = 'archived_chats_user_id_fkey'
          AND table_name = 'archived_chats'
    ) THEN
        ALTER TABLE archived_chats
            ADD CONSTRAINT archived_chats_user_id_fkey
            FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE SET NULL;
    END IF;
END $$;

-- 3. Push subscriptions table (for admin browser notifications)
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS push_subscriptions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_id UUID NOT NULL,
    endpoint TEXT NOT NULL UNIQUE,
    p256dh TEXT NOT NULL,
    auth TEXT NOT NULL,
    subscription JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_push_subscriptions_admin_id
    ON push_subscriptions (admin_id);

-- 4. Admin settings table for storing per-admin preferences
CREATE TABLE IF NOT EXISTS admin_settings (
    admin_id UUID PRIMARY KEY,
    preferred_language TEXT NOT NULL DEFAULT 'ru',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
