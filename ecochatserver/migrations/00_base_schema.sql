-- ecoChat base schema (reconstructed 2026-05-02).
--
-- The original schema lived only on the abandoned Railway DB; the repo
-- shipped only ALTER-style patch migrations that assumed `chats`,
-- `messages`, `archived_chats`, `clients`, `users`, `push_subscriptions`,
-- `roles` already existed. This file contains the full base schema, with
-- every column referenced from `ecochatserver/database/queries/**/*.go`
-- and every constraint/index that the in-tree patch migrations expect.
--
-- Idempotent: every CREATE/ALTER uses IF NOT EXISTS. Safe to re-run.
-- Apply order on a fresh DB (the application also calls
-- `ensureDirectorTables()` on first boot for Director-related tables —
-- those are NOT included here):
--
--   1. 00_base_schema.sql                                 (this file)
--   2. migrations/20251014_add_users_and_archive_columns.sql
--      — already idempotent; re-applies its IF-NOT-EXISTS clauses.
--   3. ecochatserver/migrations/add_moooving_chat_support.sql
--      — moooving columns/indexes; re-applies cleanly.
--   4. ecochatserver/database/migrations_fixed.sql
--      — installs `insert_message_safe` SP + adds the `check_message_timestamp_reasonable`
--        constraint. Re-applies cleanly.

BEGIN;

-- pgcrypto provides gen_random_uuid()/digest()/crypt() used throughout.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ─────────────────────────────────────────────────────────────────────────────
-- clients — owner / tenant of chats. Created on demand via
-- EnsureClientWithAPIKey (queries/client.go). API key acts as the lookup key.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS clients (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT         NOT NULL,
    api_key      TEXT         NOT NULL UNIQUE,
    subscription TEXT         NOT NULL DEFAULT 'free',
    active       BOOLEAN      NOT NULL DEFAULT true,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- ─────────────────────────────────────────────────────────────────────────────
-- roles — admin role definitions. ecoChat admin login resolves
-- users.role_id → roles.name. The 2025-10-12 admins-to-users migration also
-- expects this table.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS roles (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT         NOT NULL UNIQUE,
    description TEXT,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

INSERT INTO roles (name, description) VALUES
    ('super_admin', 'Full access to every chat and admin function'),
    ('supervisor',  'Sees a configurable subset of sources / clients'),
    ('manager',     'Operates under a supervisor'),
    ('user',        'Default role for unprivileged users')
ON CONFLICT (name) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────────
-- users — chat counterparties AND admin accounts (see migrate_admins_to_users).
-- `name` is nullable because some queries populate `display_name` instead;
-- one of the two is always present.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS users (
    id              UUID         PRIMARY KEY,
    email           TEXT,
    email_verified  BOOLEAN      NOT NULL DEFAULT false,
    name            TEXT,                  -- nullable: chat-side counterparty name
    display_name    TEXT,                  -- admin-side display name
    avatar          TEXT,
    avatar_url      TEXT,
    profile_url     TEXT,
    password_hash   TEXT,
    role_id         UUID         REFERENCES roles (id) ON DELETE SET NULL,
    status          TEXT         NOT NULL DEFAULT 'active',
    source          TEXT,
    source_id       TEXT,
    last_login_at   TIMESTAMPTZ,
    deleted_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Full unique index (NOT partial). chat_moooving.go runs:
--     INSERT INTO users (...) ON CONFLICT (source, source_id) DO UPDATE
-- which Postgres only matches against an unconditional unique constraint.
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_source_source_id
    ON users (source, source_id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email_unique
    ON users (LOWER(email))
    WHERE email IS NOT NULL AND deleted_at IS NULL;

-- ─────────────────────────────────────────────────────────────────────────────
-- chats — one row per conversation. The moooving integration adds
-- order_id / client_id_ext / driver_id_ext via add_moooving_chat_support.sql;
-- we declare them up-front so a fresh boot doesn't depend on patch ordering.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS chats (
    id                       UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id                  UUID         NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    client_id                UUID         NOT NULL REFERENCES clients (id) ON DELETE CASCADE,
    status                   TEXT         NOT NULL DEFAULT 'active',
    source                   TEXT         NOT NULL,
    bot_id                   TEXT         NOT NULL DEFAULT '',
    widget_business_id       TEXT,
    assigned_to              UUID         REFERENCES users (id) ON DELETE SET NULL,
    auto_responder_enabled   BOOLEAN      NOT NULL DEFAULT true,
    is_archived              BOOLEAN      NOT NULL DEFAULT false,
    archive_timer_paused     BOOLEAN      NOT NULL DEFAULT false,
    resolved_at              TIMESTAMPTZ,
    auto_archive_at          TIMESTAMPTZ,
    metadata                 JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at               TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    -- moooving integration
    order_id                 BIGINT,
    client_id_ext            BIGINT,
    driver_id_ext            BIGINT
);

CREATE INDEX IF NOT EXISTS idx_chats_status_updated_at
    ON chats (status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_chats_client_id_active
    ON chats (client_id, updated_at DESC)
    WHERE is_archived = false;
CREATE INDEX IF NOT EXISTS idx_chats_assigned_to
    ON chats (assigned_to)
    WHERE assigned_to IS NOT NULL AND is_archived = false;
CREATE INDEX IF NOT EXISTS idx_chats_user_id_active
    ON chats (user_id, updated_at DESC)
    WHERE is_archived = false;
CREATE UNIQUE INDEX IF NOT EXISTS uq_chats_active_order_id
    ON chats (order_id)
    WHERE order_id IS NOT NULL AND is_archived = false;
CREATE INDEX IF NOT EXISTS idx_chats_driver_id_ext_active
    ON chats (driver_id_ext, updated_at DESC)
    WHERE driver_id_ext IS NOT NULL AND is_archived = false;
CREATE INDEX IF NOT EXISTS idx_chats_client_id_ext_active
    ON chats (client_id_ext, updated_at DESC)
    WHERE client_id_ext IS NOT NULL AND is_archived = false;

-- ─────────────────────────────────────────────────────────────────────────────
-- messages — partitioned by timestamp (weekly RANGE). The application calls
-- public.create_future_partitions(8) on startup to roll partitions forward.
-- The PK includes "timestamp" because Postgres requires the partition key in
-- every unique constraint on a partitioned table.
--
-- metadata / content_hash / created_minute are intentionally NULLABLE: the
-- DEFAULT '{}' on a parent table is NOT inherited by partitions in Postgres,
-- so making them NOT NULL would break inserts on freshly-created partitions
-- whenever the application doesn't pass an explicit value.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS messages (
    id              UUID         NOT NULL DEFAULT gen_random_uuid(),
    chat_id         UUID         NOT NULL,
    sender          TEXT         NOT NULL,
    sender_id       UUID         NOT NULL,
    content         TEXT         NOT NULL,
    type            TEXT         NOT NULL DEFAULT 'text',
    metadata        JSONB,
    read            BOOLEAN      NOT NULL DEFAULT false,
    read_at         TIMESTAMPTZ,
    content_hash    TEXT,
    created_minute  TIMESTAMP,
    "timestamp"     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (id, "timestamp")
) PARTITION BY RANGE ("timestamp");

CREATE INDEX IF NOT EXISTS idx_messages_chat_timestamp
    ON messages (chat_id, "timestamp" DESC);
CREATE INDEX IF NOT EXISTS idx_messages_chat_unread
    ON messages (chat_id)
    WHERE read = false;

-- Per-sender partial unique indexes used for content-based deduplication.
-- migrations_fixed.sql installs the same indexes for 'user'/'admin' and
-- add_moooving_chat_support.sql installs the 'driver' one — declared here
-- so first boot has all three even before patch migrations run.
CREATE UNIQUE INDEX IF NOT EXISTS unique_user_message_with_timestamp
    ON messages (chat_id, sender_id, content_hash, "timestamp")
    WHERE sender = 'user';
CREATE UNIQUE INDEX IF NOT EXISTS unique_admin_message_with_timestamp
    ON messages (chat_id, sender_id, content_hash, "timestamp")
    WHERE sender = 'admin';
CREATE UNIQUE INDEX IF NOT EXISTS unique_driver_message_with_timestamp
    ON messages (chat_id, sender_id, content_hash, "timestamp")
    WHERE sender = 'driver';

-- Default partition catches any timestamp outside the explicitly-managed
-- weekly windows. Stops INSERTs from failing if create_future_partitions
-- hasn't run yet (e.g. very first boot before the startup hook fires).
CREATE TABLE IF NOT EXISTS messages_default
    PARTITION OF messages DEFAULT;

-- ─────────────────────────────────────────────────────────────────────────────
-- create_future_partitions — referenced by db.go on startup as
-- ensurePartitions(8). Creates weekly partitions named messages_YYYY_IW for
-- the next N weeks (idempotent via IF NOT EXISTS).
-- ─────────────────────────────────────────────────────────────────────────────
CREATE OR REPLACE FUNCTION public.create_future_partitions(weeks_ahead integer)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    week_offset INTEGER;
    start_ts    TIMESTAMPTZ;
    end_ts      TIMESTAMPTZ;
    part_name   TEXT;
BEGIN
    FOR week_offset IN 0..(weeks_ahead - 1) LOOP
        -- Postgres date_trunc('week', ...) snaps to Monday 00:00 UTC.
        start_ts := date_trunc('week', NOW()) + make_interval(weeks => week_offset);
        end_ts   := start_ts + interval '1 week';
        part_name := format('messages_%s', to_char(start_ts, 'IYYY_IW'));

        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF messages FOR VALUES FROM (%L) TO (%L)',
            part_name, start_ts, end_ts
        );
    END LOOP;
END;
$$;

-- ─────────────────────────────────────────────────────────────────────────────
-- archived_chats — pre-deletion cold storage. The 2025-10-14 patch ALTERs
-- this table to add user_id / messages JSONB / delete_at / delete_timer_paused;
-- declared inline so first boot has the columns without depending on patches.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS archived_chats (
    id                    UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    chat_id               UUID         NOT NULL UNIQUE,
    client_id             UUID         NOT NULL REFERENCES clients (id) ON DELETE CASCADE,
    user_id               UUID         REFERENCES users (id) ON DELETE SET NULL,
    archived_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    delete_at             TIMESTAMPTZ,
    delete_timer_paused   BOOLEAN      NOT NULL DEFAULT false,
    messages              JSONB        NOT NULL DEFAULT '[]'::jsonb,
    metadata              JSONB        NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS idx_archived_chats_client_id
    ON archived_chats (client_id);
CREATE INDEX IF NOT EXISTS idx_archived_chats_archived_at
    ON archived_chats (archived_at DESC);
CREATE INDEX IF NOT EXISTS idx_archived_chats_delete_at
    ON archived_chats (delete_at)
    WHERE delete_timer_paused = false;

-- ─────────────────────────────────────────────────────────────────────────────
-- push_subscriptions — Web Push endpoints. Both the 2025-10-14 migration
-- (CREATE TABLE) and add_moooving_chat_support.sql (ADD source/ext_user_id)
-- target this table. We declare it complete here.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS push_subscriptions (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_id     UUID,
    endpoint     TEXT         NOT NULL UNIQUE,
    p256dh       TEXT         NOT NULL,
    auth         TEXT         NOT NULL,
    source       TEXT,
    ext_user_id  BIGINT,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_push_subscriptions_admin_id
    ON push_subscriptions (admin_id)
    WHERE admin_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_push_subscriptions_moooving
    ON push_subscriptions (source, ext_user_id)
    WHERE source IS NOT NULL;

COMMIT;

-- Bootstrap weekly partitions for the next 8 weeks (matches db.go default).
SELECT public.create_future_partitions(8);
