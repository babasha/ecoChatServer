package models

import (
	"time"

	"github.com/google/uuid"
)

// DirectorIdentity represents an aspect of the Director's personality/identity.
// Each aspect (soul, goals, style, etc.) is stored as a separate row.
type DirectorIdentity struct {
	ID           uuid.UUID `json:"id"`
	Key          string    `json:"key"`           // soul, identity, goals, style, values, user_profile, self_assessment
	Content      string    `json:"content"`       // free-form text defining this aspect
	Version      int       `json:"version"`       // incremented on each update
	UpdatedAt    time.Time `json:"updated_at"`
	UpdatedBy    string    `json:"updated_by"`    // system, director, admin
	ChangeReason string    `json:"change_reason"` // why it was changed
	CreatedAt    time.Time `json:"created_at"`
}

// DirectorIdentityHistory represents a previous version of an identity aspect.
type DirectorIdentityHistory struct {
	ID           uuid.UUID `json:"id"`
	IdentityKey  string    `json:"identity_key"`
	Version      int       `json:"version"`
	Content      string    `json:"content"`
	ChangedBy    string    `json:"changed_by"`
	ChangeReason string    `json:"change_reason"`
	CreatedAt    time.Time `json:"created_at"`
}

// Identity aspect keys
const (
	IdentityKeySoul           = "soul"            // Core: who I am, my principles, philosophy
	IdentityKeyIdentity       = "identity"        // Name, archetype, vibe, emoji
	IdentityKeyGoals          = "goals"           // Current goals and priorities
	IdentityKeyStyle          = "style"           // Communication style: tone, format, language
	IdentityKeyValues         = "values"          // Values and rules: what matters, what's forbidden
	IdentityKeyUserProfile    = "user_profile"    // Admin profile: who, preferences, context
	IdentityKeySelfAssessment = "self_assessment" // Self-assessment: strengths, weaknesses
)
