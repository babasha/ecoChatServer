package handlers

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// generateMessageID создает детерминированный UUID для дедупликации
func generateMessageID(chatID, senderID uuid.UUID, content string, timestamp time.Time) uuid.UUID {
	data := fmt.Sprintf("%s:%s:%s:%d", 
		chatID.String(), senderID.String(), content,
		timestamp.UTC().Truncate(time.Minute).Unix())
	
	hash := sha256.Sum256([]byte(data))
	var uuidBytes [16]byte
	copy(uuidBytes[:], hash[:16])
	uuidBytes[6] = (uuidBytes[6] & 0x0f) | 0x40 // UUID v4
	uuidBytes[8] = (uuidBytes[8] & 0x3f) | 0x80
	return uuid.UUID(uuidBytes)
}