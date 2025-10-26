-- Migration: Add instagram_accounts table for OAuth tokens
-- Date: 2025-10-23
-- Description: Stores Instagram Business Account OAuth tokens and metadata

CREATE TABLE IF NOT EXISTS instagram_accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    instagram_account_id VARCHAR(255) UNIQUE NOT NULL,
    username VARCHAR(255) NOT NULL,
    name VARCHAR(255),
    profile_picture_url TEXT,
    page_id VARCHAR(255) NOT NULL,
    page_name VARCHAR(255),
    access_token TEXT NOT NULL,
    token_expires_at TIMESTAMPTZ NOT NULL,
    status VARCHAR(50) DEFAULT 'active' CHECK (status IN ('active', 'disconnected', 'expired')),
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Index для быстрого поиска активных аккаунтов
CREATE INDEX IF NOT EXISTS idx_instagram_accounts_status
ON instagram_accounts(status, updated_at DESC);

-- Index для поиска по Instagram Account ID
CREATE INDEX IF NOT EXISTS idx_instagram_accounts_instagram_id
ON instagram_accounts(instagram_account_id);

-- Комментарии для документации
COMMENT ON TABLE instagram_accounts IS 'Stores Instagram Business Account OAuth tokens and metadata';
COMMENT ON COLUMN instagram_accounts.instagram_account_id IS 'Instagram Business Account ID from Meta API';
COMMENT ON COLUMN instagram_accounts.access_token IS 'Page Access Token for Instagram Business Account (encrypted in production)';
COMMENT ON COLUMN instagram_accounts.token_expires_at IS 'When the access token expires (typically 60 days)';
COMMENT ON COLUMN instagram_accounts.status IS 'Account status: active, disconnected, or expired';
