package queries

import (
	"database/sql"
	"fmt"

	"github.com/egor/ecochatserver/models"
	"golang.org/x/crypto/bcrypt"
)

// getAdminByField ищет админа по указанному полю (email или id).
// field должен быть "u.email" или "u.id".
func getAdminByField(db *sql.DB, field, value, logPrefix string) (*models.Admin, error) {
	// Валидация допустимых полей для защиты от SQL injection
	switch field {
	case "u.email", "u.id":
	default:
		return nil, fmt.Errorf("%s: invalid field %q", logPrefix, field)
	}

	ctx, cancel := WithDBContext()
	defer cancel()

	var admin models.Admin

	q := fmt.Sprintf(`
        SELECT
            u.id,
            u.email,
            u.display_name,
            u.password_hash,
            u.avatar_url,
            u.status,
            u.email_verified,
            u.role_id,
            COALESCE(r.name, 'user') as role_name
        FROM users u
        LEFT JOIN roles r ON r.id = u.role_id
        WHERE %s = $1 AND u.deleted_at IS NULL`, field)

	if err := db.QueryRowContext(ctx, q, value).Scan(
		&admin.ID,
		&admin.Email,
		&admin.Name,
		&admin.PasswordHash,
		&admin.Avatar,
		&admin.Status,
		&admin.EmailVerified,
		&admin.RoleID,
		&admin.Role,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("%s: %w", logPrefix, err)
	}

	return &admin, nil
}

func GetAdmin(db *sql.DB, email string) (*models.Admin, error) {
	return getAdminByField(db, "u.email", email, "GetAdmin")
}

func GetAdminByID(db *sql.DB, adminID string) (*models.Admin, error) {
	return getAdminByField(db, "u.id", adminID, "GetAdminByID")
}

func VerifyPassword(pw, hash string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw))
}
