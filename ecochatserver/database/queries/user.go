package queries

import (
    "context"
    "database/sql"
    "log"
    "time"

    "github.com/google/uuid"
    "github.com/egor/ecochatserver/models"
)

func getOrCreateUser(
    ctx context.Context, tx *sql.Tx,
    userID, userName, userEmail, source, sourceID string,
) (*models.User, error) {
    var user models.User
    var avatarNull sql.NullString

    err := tx.QueryRowContext(ctx,
        "SELECT id,name,email,avatar,source,source_id FROM users WHERE source=$1 AND source_id=$2 LIMIT 1",
        source, sourceID,
    ).Scan(&user.ID, &user.Name, &user.Email, &avatarNull, &user.Source, &user.SourceID)
    if err != nil && err != sql.ErrNoRows {
        return nil, err
    }
    if err == nil {
        user.Avatar = nullStringToPointer(avatarNull)
        return &user, nil
    }

    user.ID = uuid.New()
    if parsed, err := uuid.Parse(userID); err == nil {
        user.ID = parsed
    }
    user.Name, user.Email, user.Source, user.SourceID = userName, userEmail, source, sourceID
    if _, err := tx.ExecContext(ctx,
        "INSERT INTO users(id,name,email,source,source_id,created_at) VALUES($1,$2,$3,$4,$5,$6)",
        user.ID, user.Name, user.Email, user.Source, user.SourceID, time.Now(),
    ); err != nil {
        return nil, err
    }
    log.Printf("Created user %s from %s/%s", user.ID, source, sourceID)
    return &user, nil
}