package queries

import (
    "context"
    "database/sql"
    "fmt"
    
    "golang.org/x/crypto/bcrypt"
    "github.com/egor/ecochatserver/database"
    "github.com/egor/ecochatserver/models"
)

func GetAdmin(email string) (*models.Admin, error) {
    ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
    defer cancel()

    var admin models.Admin
    var avatarNull sql.NullString

    const q = `
        SELECT id,name,email,password_hash,avatar,role,client_id,active
          FROM admins
         WHERE email=$1`
    if err := database.DB.QueryRowContext(ctx, q, email).Scan(
        &admin.ID, &admin.Name, &admin.Email, &admin.PasswordHash,
        &avatarNull, &admin.Role, &admin.ClientID, &admin.Active,
    ); err != nil {
        if err == sql.ErrNoRows {
            return nil, nil
        }
        return nil, fmt.Errorf("GetAdmin: %w", err)
    }
    admin.Avatar = nullStringToPointer(avatarNull)
    return &admin, nil
}

func VerifyPassword(pw, hash string) error {
    return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw))
}