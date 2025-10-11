package queries

import (
	"database/sql"
	"fmt"

	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func GetAdmin(db *sql.DB, email string) (*models.Admin, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	var admin models.Admin

	const q = `
        SELECT id, name, email, password, role
          FROM admins
         WHERE email=$1`

	fmt.Printf("[GetAdmin] Executing query for email: %s\n", email)

	if err := db.QueryRowContext(ctx, q, email).Scan(
		&admin.ID, &admin.Name, &admin.Email, &admin.PasswordHash, &admin.Role,
	); err != nil {
		if err == sql.ErrNoRows {
			fmt.Printf("[GetAdmin] No rows found for email: %s\n", email)
			return nil, nil
		}
		fmt.Printf("[GetAdmin] Error: %v\n", err)
		return nil, fmt.Errorf("GetAdmin: %w", err)
	}

	// По умолчанию аккаунты активны (колонка active отсутствует в БД)
	admin.Active = true

	// ClientID по умолчанию - nil UUID (эти колонки отсутствуют в БД)
	admin.ClientID = uuid.Nil
	admin.Avatar = nil

	fmt.Printf("[GetAdmin] Found admin: id=%s, email=%s, role=%s\n", admin.ID, admin.Email, admin.Role)

	return &admin, nil
}

func VerifyPassword(pw, hash string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw))
}
