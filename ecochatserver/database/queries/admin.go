package queries

import (
	"database/sql"
	"fmt"

	"github.com/egor/ecochatserver/models"
	"golang.org/x/crypto/bcrypt"
)

func GetAdmin(db *sql.DB, email string) (*models.Admin, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	var admin models.Admin

	// Используем таблицу users с JOIN на roles
	const q = `
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
        WHERE u.email = $1 AND u.deleted_at IS NULL`

	fmt.Printf("[GetAdmin] Executing query for email: %s\n", email)

	if err := db.QueryRowContext(ctx, q, email).Scan(
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
			fmt.Printf("[GetAdmin] No rows found for email: %s\n", email)
			return nil, nil
		}
		fmt.Printf("[GetAdmin] Error: %v\n", err)
		return nil, fmt.Errorf("GetAdmin: %w", err)
	}

	fmt.Printf("[GetAdmin] Found user: id=%s, email=%s, role=%s, status=%s\n", admin.ID, admin.Email, admin.Role, admin.Status)

	return &admin, nil
}

func GetAdminByID(db *sql.DB, adminID string) (*models.Admin, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	var admin models.Admin

	// Используем таблицу users с JOIN на roles
	const q = `
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
        WHERE u.id = $1 AND u.deleted_at IS NULL`

	fmt.Printf("[GetAdminByID] Executing query for id: %s\n", adminID)

	if err := db.QueryRowContext(ctx, q, adminID).Scan(
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
			fmt.Printf("[GetAdminByID] No rows found for id: %s\n", adminID)
			return nil, nil
		}
		fmt.Printf("[GetAdminByID] Error: %v\n", err)
		return nil, fmt.Errorf("GetAdminByID: %w", err)
	}

	fmt.Printf("[GetAdminByID] Found user: id=%s, email=%s, role=%s, status=%s\n", admin.ID, admin.Email, admin.Role, admin.Status)

	return &admin, nil
}

func VerifyPassword(pw, hash string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw))
}
