package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// PushSubscription описывает подписку администратора на push-уведомления
type PushSubscription struct {
	ID           uuid.UUID       `json:"id"`
	AdminID      uuid.UUID       `json:"adminId"`
	Endpoint     string          `json:"endpoint"`
	P256dh       string          `json:"p256dh"`
	Auth         string          `json:"auth"`
	Subscription json.RawMessage `json:"subscription,omitempty"`
	CreatedAt    time.Time       `json:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
	LastUsedAt   time.Time       `json:"lastUsedAt"`
}
