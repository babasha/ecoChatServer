package queries

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// ClientProfile accumulates per-user knowledge across conversations.
// Keyed by chat.User.ID (ecochat users table UUID).
type ClientProfile struct {
	UserID           uuid.UUID
	DeviceModel      string    // last known device model
	Language         string    // preferred language (ru, en, de, ...)
	CommonIssues     []string  // up to 10 most recent issue types (prepend, no dedup in DB)
	LastSentiment    string    // last interaction sentiment
	InteractionCount int       // total summarized interactions
	LastSeenAt       *time.Time
	UpdatedAt        time.Time
	CreatedAt        time.Time
}

// ClientProfileUpdate carries the fields to upsert for a given user.
// Empty strings are ignored (field not updated) except UserID which is required.
type ClientProfileUpdate struct {
	UserID        uuid.UUID
	DeviceModel   string // e.g. "Zefir Pro"
	Language      string // e.g. "ru"
	NewIssue      string // appended to common_issues (e.g. "firmware")
	LastSentiment string // e.g. "frustrated"
}

// UpsertClientProfile inserts or updates a client profile.
// On conflict: non-empty fields overwrite previous values; interaction_count increments.
// common_issues: new issue prepended, array capped at 10.
func UpsertClientProfile(db *sql.DB, u ClientProfileUpdate) error {
	ctx, cancel := WithDBContext()
	defer cancel()

	_, err := db.ExecContext(ctx, `
		INSERT INTO client_profiles
			(user_id, device_model, language, common_issues, last_sentiment,
			 interaction_count, last_seen_at, created_at, updated_at)
		VALUES (
			$1, $2, $3,
			CASE WHEN $4 != '' THEN ARRAY[$4::text] ELSE '{}'::text[] END,
			$5, 1, NOW(), NOW(), NOW()
		)
		ON CONFLICT (user_id) DO UPDATE SET
			device_model      = CASE WHEN $2 != '' THEN $2
			                         ELSE client_profiles.device_model END,
			language          = CASE WHEN $3 != '' THEN $3
			                         ELSE client_profiles.language END,
			common_issues     = CASE WHEN $4 != ''
			                         THEN (array_prepend($4::text, client_profiles.common_issues))[1:10]
			                         ELSE client_profiles.common_issues END,
			last_sentiment    = CASE WHEN $5 != '' THEN $5
			                         ELSE client_profiles.last_sentiment END,
			interaction_count = client_profiles.interaction_count + 1,
			last_seen_at      = NOW(),
			updated_at        = NOW()`,
		u.UserID, u.DeviceModel, u.Language, u.NewIssue, u.LastSentiment,
	)
	if err != nil {
		return fmt.Errorf("upsert client profile %s: %w", u.UserID, err)
	}
	return nil
}

// GetClientProfile fetches the accumulated profile for a user.
// Returns nil, nil if no profile exists yet.
func GetClientProfile(db *sql.DB, userID uuid.UUID) (*ClientProfile, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	var p ClientProfile
	err := db.QueryRowContext(ctx, `
		SELECT user_id, device_model, language, common_issues,
		       last_sentiment, interaction_count, last_seen_at, updated_at, created_at
		FROM client_profiles
		WHERE user_id = $1`,
		userID,
	).Scan(
		&p.UserID, &p.DeviceModel, &p.Language,
		pq.Array(&p.CommonIssues),
		&p.LastSentiment, &p.InteractionCount,
		&p.LastSeenAt, &p.UpdatedAt, &p.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get client profile %s: %w", userID, err)
	}
	return &p, nil
}
