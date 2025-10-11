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

	const q = `
        SELECT id, name, email, password, role
          FROM admins
         WHERE email=$1`
	if err := db.QueryRowContext(ctx, q, email).Scan(
		&admin.ID, &admin.Name, &admin.Email, &admin.PasswordHash, &admin.Role,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("GetAdmin: %w", err)
	}

	// По умолчанию аккаунты активны (колонка active отсутствует в БД)
	admin.Active = true

	return &admin, nil
}

func VerifyPassword(pw, hash string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw))
}
