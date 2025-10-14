package queries

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"time"

	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// UpsertPushSubscription сохраняет или обновляет подписку на push-уведомления.
func UpsertPushSubscription(db *sql.DB, adminID uuid.UUID, endpoint, p256dh, auth string, subscription json.RawMessage) error {
	ctx, cancel := WithDBContext()
	defer cancel()

	log.Printf("UpsertPushSubscription: admin=%s endpoint=%s", adminID, endpoint)

	_, err := db.ExecContext(ctx, `
		INSERT INTO push_subscriptions (admin_id, endpoint, p256dh, auth, subscription)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (endpoint)
		DO UPDATE SET
			admin_id     = EXCLUDED.admin_id,
			p256dh       = EXCLUDED.p256dh,
			auth         = EXCLUDED.auth,
			subscription = EXCLUDED.subscription,
			updated_at   = NOW(),
			last_used_at = NOW()
	`, adminID, endpoint, p256dh, auth, subscription)
	if err != nil {
		log.Printf("UpsertPushSubscription: error %v", err)
	}
	return err
}

// DeletePushSubscription удаляет подписку по endpoint и adminID.
func DeletePushSubscription(db *sql.DB, adminID uuid.UUID, endpoint string) error {
	ctx, cancel := WithDBContext()
	defer cancel()

	log.Printf("DeletePushSubscription: admin=%s endpoint=%s", adminID, endpoint)

	_, err := db.ExecContext(ctx, `
		DELETE FROM push_subscriptions
		WHERE admin_id = $1 AND endpoint = $2
	`, adminID, endpoint)
	if err != nil {
		log.Printf("DeletePushSubscription: error %v", err)
	}
	return err
}

// DeletePushSubscriptionByEndpoint удаляет подписку без проверки adminID (используется для инвалидных подписок).
func DeletePushSubscriptionByEndpoint(db *sql.DB, endpoint string) error {
	ctx, cancel := WithDBContext()
	defer cancel()

	log.Printf("DeletePushSubscriptionByEndpoint: endpoint=%s", endpoint)

	_, err := db.ExecContext(ctx, `
		DELETE FROM push_subscriptions
		WHERE endpoint = $1
	`, endpoint)
	if err != nil {
		log.Printf("DeletePushSubscriptionByEndpoint: error %v", err)
	}
	return err
}

// ListPushSubscriptions возвращает все подписки администратора.
func ListPushSubscriptions(db *sql.DB, adminID uuid.UUID) ([]models.PushSubscription, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	rows, err := db.QueryContext(ctx, `
		SELECT id, admin_id, endpoint, p256dh, auth, subscription, created_at, updated_at, last_used_at
		FROM push_subscriptions
		WHERE admin_id = $1
		ORDER BY updated_at DESC
	`, adminID)
	if err != nil {
		log.Printf("ListPushSubscriptions: query error %v", err)
		return nil, err
	}
	defer rows.Close()

	var result []models.PushSubscription
	for rows.Next() {
		var (
			sub        models.PushSubscription
			rawPayload []byte
		)
		if err := rows.Scan(
			&sub.ID,
			&sub.AdminID,
			&sub.Endpoint,
			&sub.P256dh,
			&sub.Auth,
			&rawPayload,
			&sub.CreatedAt,
			&sub.UpdatedAt,
			&sub.LastUsedAt,
		); err != nil {
			log.Printf("ListPushSubscriptions: scan error %v", err)
			return nil, err
		}

		if len(rawPayload) > 0 {
			sub.Subscription = json.RawMessage(rawPayload)
		}
		result = append(result, sub)
	}

	if err := rows.Err(); err != nil {
		log.Printf("ListPushSubscriptions: rows error %v", err)
		return nil, err
	}

	return result, nil
}

// TouchPushSubscription обновляет last_used_at для подписки.
func TouchPushSubscription(db *sql.DB, endpoint string) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	_, err := db.ExecContext(ctx, `
		UPDATE push_subscriptions
		SET last_used_at = $2, updated_at = $2
		WHERE endpoint = $1
	`, endpoint, time.Now())
	if err != nil {
		log.Printf("TouchPushSubscription: error %v", err)
	}
	return err
}
