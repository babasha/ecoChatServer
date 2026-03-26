// internal/database/db.go
package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	// pgx-драйвер в режиме database/sql
	_ "github.com/jackc/pgx/v5/stdlib"
)

var DB *sql.DB      // БД для чатов (caboose)
var UsersDB *sql.DB // БД для клиентов и админов (ballast)

// GetDB возвращает основное подключение к БД (для обратной совместимости).
func GetDB() *sql.DB {
	return DB
}

// Init открывает пул соединений и проверяет подключение.
func Init() error {
	// Подключаемся к основной БД (чаты)
	dsn := buildDSN()
	var err error

	DB, err = sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("sql.Open (main): %w", err)
	}

	// Параметры пула
	DB.SetMaxOpenConns(25)
	DB.SetMaxIdleConns(5)
	DB.SetConnMaxLifetime(5 * time.Minute)

	// Проверяем подключение (тайм-аут 3 с)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err = DB.PingContext(ctx); err != nil {
		_ = DB.Close()
		return fmt.Errorf("db.Ping (main): %w", err)
	}

	log.Println("[database] PostgreSQL (chats) connected ✓")

	// Подключаемся к БД пользователей (если настроена)
	usersDSN := buildUsersDSN()
	if usersDSN != "" {
		UsersDB, err = sql.Open("pgx", usersDSN)
		if err != nil {
			return fmt.Errorf("sql.Open (users): %w", err)
		}

		UsersDB.SetMaxOpenConns(10)
		UsersDB.SetMaxIdleConns(2)
		UsersDB.SetConnMaxLifetime(5 * time.Minute)

		ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel2()
		if err = UsersDB.PingContext(ctx2); err != nil {
			_ = UsersDB.Close()
			return fmt.Errorf("db.Ping (users): %w", err)
		}

		log.Println("[database] PostgreSQL (users) connected ✓")
	} else {
		// Если нет отдельной БД для пользователей, используем основную
		UsersDB = DB
		log.Println("[database] Using main DB for users")
	}

	// Создаем партиции заранее
	if err := initializePartitions(); err != nil {
		log.Printf("Warning: не удалось создать партиции: %v", err)
		// Не прерываем запуск сервера из-за партиций
	}

	// Создаём таблицы для Director AI (если не существуют)
	if err := ensureDirectorTables(); err != nil {
		log.Printf("[database] Warning: не удалось создать таблицы Director: %v", err)
	}

	// Инициализируем Redis (опционально)
	if err := InitRedis(); err != nil {
		log.Printf("[REDIS] Warning: не удалось подключиться к Redis: %v", err)
		log.Println("[REDIS] Сервер будет работать без кеширования")
		// Не прерываем запуск сервера из-за Redis
	}

	return nil
}

// ensurePartitions создаёт партиции на count недель вперед.
func ensurePartitions(count int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := DB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("get conn: %w", err)
	}
	defer conn.Close()

	_, err = conn.ExecContext(ctx, fmt.Sprintf("SELECT public.create_future_partitions(%d)", count))
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return fmt.Errorf("ensure partitions: %w", err)
	}
	return nil
}

// initializePartitions создает партиции при старте.
func initializePartitions() error {
	if err := ensurePartitions(8); err != nil {
		return err
	}
	log.Println("[database] Партиции успешно созданы")
	return nil
}

// RefreshPartitions обновляет партиции (вызывается периодически).
func RefreshPartitions() error {
	return ensurePartitions(8)
}

// Close закрывает пул (вызывайте defer database.Close()).
func Close() {
	if DB != nil {
		_ = DB.Close()
	}
	if UsersDB != nil && UsersDB != DB {
		_ = UsersDB.Close()
	}
	// Закрываем Redis
	CloseRedis()
}

// ─────────────────────────────── helpers

// buildPostgresDSN формирует DSN для PostgreSQL с логированием.
func buildPostgresDSN(prefix, logName string, defaults map[string]string) string {
	host := env(prefix+"HOST", defaults["host"])
	port := env(prefix+"PORT", defaults["port"])
	user := env(prefix+"USER", defaults["user"])
	password := os.Getenv(prefix + "PASSWORD")
	dbname := env(prefix+"DATABASE", defaults["dbname"])
	sslmode := env(prefix+"SSL_MODE", defaults["sslmode"])

	var dsn string
	if password != "" {
		dsn = fmt.Sprintf(
			"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
			host, port, user, password, dbname, sslmode,
		)
	} else {
		dsn = fmt.Sprintf(
			"host=%s port=%s user=%s dbname=%s sslmode=%s",
			host, port, user, dbname, sslmode,
		)
	}

	log.Printf("[DB] Connecting (%s): host=%s port=%s user=%s dbname=%s sslmode=%s", logName, host, port, user, dbname, sslmode)
	return dsn
}

func buildDSN() string {
	return buildPostgresDSN("PG_", "chats", map[string]string{
		"host": "localhost", "port": "5432", "user": "postgres",
		"dbname": "ecochat", "sslmode": "disable",
	})
}

func buildUsersDSN() string {
	host := os.Getenv("USERS_PG_HOST")
	if host == "" {
		return "" // Используем основную БД
	}
	return buildPostgresDSN("USERS_PG_", "users", map[string]string{
		"host": host, "port": "5432", "user": "postgres",
		"dbname": "railway", "sslmode": "require",
	})
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// ensureDirectorTables creates tables needed by Director AI if they don't exist.
func ensureDirectorTables() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ddl := `
	-- Application settings (key-value store, overrides ENV variables)
	CREATE TABLE IF NOT EXISTS app_settings (
		key        TEXT PRIMARY KEY,
		value      JSONB NOT NULL DEFAULT '""',
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS director_reports (
		id UUID PRIMARY KEY,
		report_date TIMESTAMPTZ NOT NULL,
		report_type VARCHAR(50) NOT NULL DEFAULT 'periodic',
		trigger_event TEXT DEFAULT '',
		summary_count INT NOT NULL DEFAULT 0,
		analysis TEXT NOT NULL DEFAULT '',
		directives JSONB DEFAULT '[]',
		stats JSONB DEFAULT '{}',
		customer_complaints JSONB DEFAULT '[]',
		key_observations JSONB DEFAULT '[]',
		prompt_changes JSONB DEFAULT '[]',
		expectations TEXT DEFAULT '',
		applied BOOLEAN NOT NULL DEFAULT FALSE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS interaction_metrics (
		id UUID PRIMARY KEY,
		chat_id UUID NOT NULL,
		message_id UUID,
		agent_mode VARCHAR(50) NOT NULL DEFAULT '',
		agent_name VARCHAR(100) NOT NULL DEFAULT '',
		prompt_version INT,
		tools_called JSONB DEFAULT '[]',
		tool_count INT NOT NULL DEFAULT 0,
		was_escalated BOOLEAN NOT NULL DEFAULT FALSE,
		was_empty BOOLEAN NOT NULL DEFAULT FALSE,
		response_length INT NOT NULL DEFAULT 0,
		response_time_ms INT NOT NULL DEFAULT 0,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS agent_prompts (
		id UUID PRIMARY KEY,
		agent_name VARCHAR(100) NOT NULL,
		version INT NOT NULL,
		prompt TEXT NOT NULL,
		created_by VARCHAR(50) NOT NULL DEFAULT 'human',
		parent_version INT,
		is_active BOOLEAN NOT NULL DEFAULT FALSE,
		metrics JSONB DEFAULT '{}',
		notes TEXT,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE (agent_name, version)
	);

	CREATE TABLE IF NOT EXISTS chat_summaries (
		id UUID PRIMARY KEY,
		chat_id UUID NOT NULL,
		summary TEXT NOT NULL,
		messages_from TIMESTAMPTZ NOT NULL,
		messages_to TIMESTAMPTZ NOT NULL,
		message_count INT NOT NULL DEFAULT 0,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_director_reports_created_at ON director_reports (created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_interaction_metrics_created_at ON interaction_metrics (created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_interaction_metrics_agent_name ON interaction_metrics (agent_name);
	CREATE INDEX IF NOT EXISTS idx_interaction_metrics_chat_id ON interaction_metrics (chat_id);
	CREATE INDEX IF NOT EXISTS idx_agent_prompts_agent_active ON agent_prompts (agent_name, is_active);
	CREATE INDEX IF NOT EXISTS idx_chat_summaries_chat_id ON chat_summaries (chat_id);
	CREATE INDEX IF NOT EXISTS idx_chat_summaries_created_at ON chat_summaries (created_at DESC);

	CREATE TABLE IF NOT EXISTS director_memories (
		id UUID PRIMARY KEY,
		category VARCHAR(20) NOT NULL,
		key TEXT NOT NULL,
		content TEXT NOT NULL,
		importance SMALLINT NOT NULL DEFAULT 5,
		access_count INT NOT NULL DEFAULT 0,
		source VARCHAR(50) NOT NULL DEFAULT 'chat',
		tags TEXT[] DEFAULT '{}',
		pinned BOOLEAN NOT NULL DEFAULT FALSE,
		embedding float8[],
		search_vector tsvector,
		expires_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE(category, key)
	);

	-- Add embedding column if table already exists (migration)
	DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='director_memories' AND column_name='embedding') THEN
			ALTER TABLE director_memories ADD COLUMN embedding float8[];
		END IF;
	END $$;

	CREATE INDEX IF NOT EXISTS idx_director_memories_category ON director_memories (category);
	CREATE INDEX IF NOT EXISTS idx_director_memories_importance ON director_memories (importance DESC);
	CREATE INDEX IF NOT EXISTS idx_director_memories_tags ON director_memories USING GIN (tags);
	CREATE INDEX IF NOT EXISTS idx_director_memories_updated_at ON director_memories (updated_at DESC);
	CREATE INDEX IF NOT EXISTS idx_director_memories_expires ON director_memories (expires_at) WHERE expires_at IS NOT NULL;
	CREATE INDEX IF NOT EXISTS idx_director_memories_fts ON director_memories USING GIN (search_vector);

	-- Auto-update search_vector on insert/update
	CREATE OR REPLACE FUNCTION director_memories_search_trigger() RETURNS trigger AS $$
	BEGIN
		NEW.search_vector := to_tsvector('russian', coalesce(NEW.content, '') || ' ' || coalesce(NEW.key, ''));
		RETURN NEW;
	END
	$$ LANGUAGE plpgsql;
	DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_director_memories_search') THEN
			CREATE TRIGGER trg_director_memories_search
				BEFORE INSERT OR UPDATE OF content, key ON director_memories
				FOR EACH ROW EXECUTE FUNCTION director_memories_search_trigger();
		END IF;
	END $$;

	-- FTS for director_reports (search old reports)
	DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='director_reports' AND column_name='search_vector') THEN
			ALTER TABLE director_reports ADD COLUMN search_vector tsvector;
		END IF;
	END $$;
	CREATE INDEX IF NOT EXISTS idx_director_reports_fts ON director_reports USING GIN (search_vector);

	CREATE OR REPLACE FUNCTION director_reports_search_trigger() RETURNS trigger AS $$
	BEGIN
		NEW.search_vector := to_tsvector('russian',
			coalesce(NEW.analysis, '') || ' ' ||
			coalesce(NEW.expectations, '') || ' ' ||
			coalesce(NEW.trigger_event, ''));
		RETURN NEW;
	END
	$$ LANGUAGE plpgsql;
	DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_director_reports_search') THEN
			CREATE TRIGGER trg_director_reports_search
				BEFORE INSERT OR UPDATE OF analysis, expectations, trigger_event ON director_reports
				FOR EACH ROW EXECUTE FUNCTION director_reports_search_trigger();
		END IF;
	END $$;

	CREATE TABLE IF NOT EXISTS directive_outcomes (
		id UUID PRIMARY KEY,
		report_id UUID NOT NULL,
		directive_type VARCHAR(50) NOT NULL,
		directive_instruction TEXT NOT NULL,
		escalation_rate_before FLOAT,
		empty_rate_before FLOAT,
		avg_response_ms_before FLOAT,
		escalation_rate_after FLOAT,
		empty_rate_after FLOAT,
		avg_response_ms_after FLOAT,
		effectiveness VARCHAR(20) NOT NULL DEFAULT 'pending',
		evaluation_notes TEXT,
		evaluated_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_directive_outcomes_report ON directive_outcomes (report_id);
	CREATE INDEX IF NOT EXISTS idx_directive_outcomes_effectiveness ON directive_outcomes (effectiveness);
	CREATE INDEX IF NOT EXISTS idx_directive_outcomes_created ON directive_outcomes (created_at DESC);

	-- FTS for chat_summaries
	DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='chat_summaries' AND column_name='search_vector') THEN
			ALTER TABLE chat_summaries ADD COLUMN search_vector tsvector;
		END IF;
	END $$;
	CREATE INDEX IF NOT EXISTS idx_chat_summaries_fts ON chat_summaries USING GIN (search_vector);

	CREATE OR REPLACE FUNCTION chat_summaries_search_trigger() RETURNS trigger AS $$
	BEGIN
		NEW.search_vector := to_tsvector('russian', coalesce(NEW.summary, ''));
		RETURN NEW;
	END
	$$ LANGUAGE plpgsql;
	DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_chat_summaries_search') THEN
			CREATE TRIGGER trg_chat_summaries_search
				BEFORE INSERT OR UPDATE OF summary ON chat_summaries
				FOR EACH ROW EXECUTE FUNCTION chat_summaries_search_trigger();
		END IF;
	END $$;

	-- Backfill search_vector for existing chat_summaries
	UPDATE chat_summaries SET search_vector = to_tsvector('russian', coalesce(summary, ''))
	WHERE search_vector IS NULL;

	-- FTS for directive_outcomes
	DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='directive_outcomes' AND column_name='search_vector') THEN
			ALTER TABLE directive_outcomes ADD COLUMN search_vector tsvector;
		END IF;
	END $$;
	CREATE INDEX IF NOT EXISTS idx_directive_outcomes_fts ON directive_outcomes USING GIN (search_vector);

	CREATE OR REPLACE FUNCTION directive_outcomes_search_trigger() RETURNS trigger AS $$
	BEGIN
		NEW.search_vector := to_tsvector('russian',
			coalesce(NEW.directive_instruction, '') || ' ' ||
			coalesce(NEW.directive_type, '') || ' ' ||
			coalesce(NEW.evaluation_notes, ''));
		RETURN NEW;
	END
	$$ LANGUAGE plpgsql;
	DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_directive_outcomes_search') THEN
			CREATE TRIGGER trg_directive_outcomes_search
				BEFORE INSERT OR UPDATE OF directive_instruction, evaluation_notes ON directive_outcomes
				FOR EACH ROW EXECUTE FUNCTION directive_outcomes_search_trigger();
		END IF;
	END $$;

	-- Backfill search_vector for existing directive_outcomes
	UPDATE directive_outcomes SET search_vector = to_tsvector('russian',
		coalesce(directive_instruction, '') || ' ' ||
		coalesce(directive_type, '') || ' ' ||
		coalesce(evaluation_notes, ''))
	WHERE search_vector IS NULL;

	-- Director skills (self-created tools)
	CREATE TABLE IF NOT EXISTS director_skills (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		name VARCHAR(100) NOT NULL UNIQUE,
		description TEXT NOT NULL,
		parameters JSONB NOT NULL DEFAULT '{}',
		skill_type VARCHAR(20) NOT NULL,
		code TEXT NOT NULL,
		version INT NOT NULL DEFAULT 1,
		enabled BOOLEAN NOT NULL DEFAULT TRUE,
		created_by VARCHAR(50) NOT NULL DEFAULT 'director',
		usage_count INT NOT NULL DEFAULT 0,
		last_used_at TIMESTAMPTZ,
		last_error TEXT,
		tags TEXT[] DEFAULT '{}',
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_director_skills_enabled ON director_skills (enabled, usage_count DESC);
	CREATE INDEX IF NOT EXISTS idx_director_skills_name ON director_skills (name);

	-- Director tool log (for self-building pattern detection)
	CREATE TABLE IF NOT EXISTS director_tool_log (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		tool_name VARCHAR(100) NOT NULL,
		arguments JSONB DEFAULT '{}',
		result_length INT DEFAULT 0,
		success BOOLEAN DEFAULT TRUE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_director_tool_log_name ON director_tool_log (tool_name, created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_director_tool_log_created ON director_tool_log (created_at DESC);

	-- Auto-cleanup: keep only last 30 days of tool log
	DELETE FROM director_tool_log WHERE created_at < NOW() - INTERVAL '30 days';

	-- Director digests (consolidated period summaries)
	CREATE TABLE IF NOT EXISTS director_digests (
		id UUID PRIMARY KEY,
		period_type VARCHAR(10) NOT NULL,
		period_start DATE NOT NULL,
		period_end DATE NOT NULL,
		total_chats INT DEFAULT 0,
		total_interactions INT DEFAULT 0,
		avg_escalation_rate FLOAT DEFAULT 0,
		avg_empty_rate FLOAT DEFAULT 0,
		avg_response_ms FLOAT DEFAULT 0,
		summary TEXT NOT NULL DEFAULT '',
		key_events TEXT[] DEFAULT '{}',
		trends TEXT[] DEFAULT '{}',
		lessons TEXT[] DEFAULT '{}',
		search_vector tsvector,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE(period_type, period_start)
	);

	CREATE INDEX IF NOT EXISTS idx_director_digests_period ON director_digests (period_type, period_start DESC);
	CREATE INDEX IF NOT EXISTS idx_director_digests_fts ON director_digests USING GIN (search_vector);

	CREATE OR REPLACE FUNCTION director_digests_search_trigger() RETURNS trigger AS $$
	BEGIN
		NEW.search_vector := to_tsvector('russian',
			coalesce(NEW.summary, '') || ' ' ||
			coalesce(array_to_string(NEW.key_events, ' '), '') || ' ' ||
			coalesce(array_to_string(NEW.trends, ' '), '') || ' ' ||
			coalesce(array_to_string(NEW.lessons, ' '), ''));
		RETURN NEW;
	END
	$$ LANGUAGE plpgsql;
	DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_director_digests_search') THEN
			CREATE TRIGGER trg_director_digests_search
				BEFORE INSERT OR UPDATE OF summary, key_events, trends, lessons ON director_digests
				FOR EACH ROW EXECUTE FUNCTION director_digests_search_trigger();
		END IF;
	END $$;

	-- Director chat messages (persistent conversation history)
	CREATE TABLE IF NOT EXISTS director_chat_messages (
		id SERIAL PRIMARY KEY,
		admin_id TEXT NOT NULL,
		role TEXT NOT NULL,
		content TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	CREATE INDEX IF NOT EXISTS idx_director_chat_admin ON director_chat_messages(admin_id, created_at);

	-- Director chat compactions (summarized old messages)
	CREATE TABLE IF NOT EXISTS director_chat_compactions (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		admin_id TEXT NOT NULL,
		summary TEXT NOT NULL,
		messages_from TIMESTAMPTZ NOT NULL,
		messages_to TIMESTAMPTZ NOT NULL,
		message_count INT NOT NULL DEFAULT 0,
		source_messages JSONB,
		previous_compaction_id UUID REFERENCES director_chat_compactions(id) ON DELETE SET NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	CREATE INDEX IF NOT EXISTS idx_director_chat_compactions_admin ON director_chat_compactions(admin_id, created_at DESC);

	-- Embedding cache (L2 persistent cache for vector embeddings)
	CREATE TABLE IF NOT EXISTS embedding_cache (
		hash        VARCHAR(64) NOT NULL,
		provider    VARCHAR(20) NOT NULL,
		model       VARCHAR(50) NOT NULL,
		dim         INT NOT NULL,
		embedding   float8[] NOT NULL,
		text_preview VARCHAR(100) DEFAULT '',
		hit_count   INT NOT NULL DEFAULT 0,
		created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		last_used   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (hash, provider, model)
	);
	CREATE INDEX IF NOT EXISTS idx_embedding_cache_last_used ON embedding_cache (last_used);

	-- Director webhook events (external trigger logging & idempotency)
	CREATE TABLE IF NOT EXISTS director_webhook_events (
		id               UUID PRIMARY KEY,
		source           VARCHAR(100) NOT NULL,
		event_type       VARCHAR(100) NOT NULL,
		message          TEXT NOT NULL DEFAULT '',
		metadata         JSONB DEFAULT '{}',
		idempotency_key  VARCHAR(255) DEFAULT '',
		action           VARCHAR(20) NOT NULL DEFAULT 'analyze',
		priority         VARCHAR(20) NOT NULL DEFAULT 'normal',
		status           VARCHAR(20) NOT NULL DEFAULT 'received',
		error            TEXT DEFAULT '',
		processed_at     TIMESTAMPTZ,
		created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	CREATE INDEX IF NOT EXISTS idx_webhook_events_idempotency ON director_webhook_events (idempotency_key) WHERE idempotency_key != '';
	CREATE INDEX IF NOT EXISTS idx_webhook_events_created ON director_webhook_events (created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_webhook_events_source ON director_webhook_events (source, event_type);

	-- Director cron jobs (flexible scheduling)
	CREATE TABLE IF NOT EXISTS director_cron_jobs (
		id             UUID PRIMARY KEY,
		name           VARCHAR(200) NOT NULL UNIQUE,
		description    TEXT DEFAULT '',
		schedule_type  VARCHAR(10) NOT NULL,  -- 'at', 'every', 'cron'
		schedule       VARCHAR(200) NOT NULL, -- ISO datetime / duration / cron expr
		timezone       VARCHAR(50) DEFAULT '',
		action         VARCHAR(50) NOT NULL DEFAULT 'analyze',
		action_config  TEXT DEFAULT '',
		enabled        BOOLEAN NOT NULL DEFAULT true,
		created_by     VARCHAR(50) DEFAULT 'system',
		last_run_at    TIMESTAMPTZ,
		next_run_at    TIMESTAMPTZ,
		run_count      INT NOT NULL DEFAULT 0,
		last_error     TEXT DEFAULT '',
		max_runs       INT NOT NULL DEFAULT 0,
		created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	CREATE INDEX IF NOT EXISTS idx_cron_jobs_enabled ON director_cron_jobs (enabled, next_run_at) WHERE enabled = true;

	-- Cron run logs (execution history)
	CREATE TABLE IF NOT EXISTS director_cron_run_log (
		id          UUID PRIMARY KEY,
		job_id      UUID NOT NULL REFERENCES director_cron_jobs(id) ON DELETE CASCADE,
		status      VARCHAR(20) NOT NULL,
		duration_ms INT DEFAULT 0,
		error       TEXT DEFAULT '',
		created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	CREATE INDEX IF NOT EXISTS idx_cron_run_log_job ON director_cron_run_log (job_id, created_at DESC);

	-- Per-client accumulated knowledge profile
	CREATE TABLE IF NOT EXISTS client_profiles (
		user_id           UUID PRIMARY KEY,
		device_model      VARCHAR(100) NOT NULL DEFAULT '',
		language          VARCHAR(10) NOT NULL DEFAULT '',
		common_issues     TEXT[] NOT NULL DEFAULT '{}',
		last_sentiment    VARCHAR(20) NOT NULL DEFAULT '',
		interaction_count INT NOT NULL DEFAULT 0,
		last_seen_at      TIMESTAMPTZ,
		updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	CREATE INDEX IF NOT EXISTS idx_client_profiles_updated ON client_profiles (updated_at DESC);

	-- Hebbian associative graph
	CREATE TABLE IF NOT EXISTS concept_nodes (
		id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		label       VARCHAR(200) NOT NULL UNIQUE,
		category    VARCHAR(30) NOT NULL DEFAULT '',
		activation  FLOAT NOT NULL DEFAULT 1.0,
		hit_count   INT NOT NULL DEFAULT 1,
		created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	CREATE INDEX IF NOT EXISTS idx_concept_nodes_label    ON concept_nodes (label);
	CREATE INDEX IF NOT EXISTS idx_concept_nodes_category ON concept_nodes (category);

	CREATE TABLE IF NOT EXISTS concept_edges (
		id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		source_id   UUID NOT NULL REFERENCES concept_nodes(id) ON DELETE CASCADE,
		target_id   UUID NOT NULL REFERENCES concept_nodes(id) ON DELETE CASCADE,
		weight      FLOAT NOT NULL DEFAULT 1.0,
		co_occur    INT NOT NULL DEFAULT 1,
		created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE(source_id, target_id)
	);
	CREATE INDEX IF NOT EXISTS idx_concept_edges_source ON concept_edges (source_id);
	CREATE INDEX IF NOT EXISTS idx_concept_edges_target ON concept_edges (target_id);
	CREATE INDEX IF NOT EXISTS idx_concept_edges_weight ON concept_edges (weight DESC);
	`

	_, err := DB.ExecContext(ctx, ddl)
	if err != nil {
		return fmt.Errorf("ensure director tables: %w", err)
	}
	log.Println("[database] Director tables ready ✓")

	// Identity tables live in UsersDB (ballast)
	if err := ensureIdentityTables(); err != nil {
		log.Printf("[database] Warning: не удалось создать таблицы Identity: %v", err)
	}

	return nil
}

// ensureIdentityTables creates identity tables in UsersDB (ballast).
func ensureIdentityTables() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ddl := `
	CREATE TABLE IF NOT EXISTS director_identity (
		id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		key         TEXT NOT NULL UNIQUE,
		content     TEXT NOT NULL,
		version     INT NOT NULL DEFAULT 1,
		updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_by  TEXT NOT NULL DEFAULT 'system',
		change_reason TEXT,
		created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS director_identity_history (
		id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		identity_key    TEXT NOT NULL,
		version         INT NOT NULL,
		content         TEXT NOT NULL,
		changed_by      TEXT NOT NULL,
		change_reason   TEXT,
		created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_identity_history_key ON director_identity_history (identity_key, version DESC);
	CREATE INDEX IF NOT EXISTS idx_identity_history_created ON director_identity_history (created_at DESC);
	`

	_, err := UsersDB.ExecContext(ctx, ddl)
	if err != nil {
		return fmt.Errorf("ensure identity tables: %w", err)
	}
	log.Println("[database] Identity tables ready ✓")
	return nil
}
