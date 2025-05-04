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
    log.Printf("getOrCreateUser: начало, userID=%s, userName=%s, source=%s, sourceID=%s", 
        userID, userName, source, sourceID)
    
    var user models.User
    var avatarNull sql.NullString

    err := tx.QueryRowContext(ctx,
        "SELECT id,name,email,avatar,source,source_id FROM users WHERE source=$1 AND source_id=$2 LIMIT 1",
        source, sourceID,
    ).Scan(&user.ID, &user.Name, &user.Email, &avatarNull, &user.Source, &user.SourceID)
    
    if err != nil && err != sql.ErrNoRows {
        log.Printf("getOrCreateUser: ошибка поиска пользователя: %v", err)
        return nil, err
    }
    
    if err == nil {
        user.Avatar = nullStringToPointer(avatarNull)
        log.Printf("getOrCreateUser: найден существующий пользователь ID=%s, name=%s", user.ID, user.Name)
        return &user, nil
    }

    // Создаем нового пользователя
    user.ID = uuid.New()
    if parsed, err := uuid.Parse(userID); err == nil {
        user.ID = parsed
        log.Printf("getOrCreateUser: используем переданный UUID: %s", user.ID)
    } else {
        log.Printf("getOrCreateUser: создан новый UUID: %s для userID=%s", user.ID, userID)
    }
    
    user.Name, user.Email, user.Source, user.SourceID = userName, userEmail, source, sourceID
    
    log.Printf("getOrCreateUser: создаем нового пользователя ID=%s, name=%s, source=%s/%s", 
        user.ID, user.Name, source, sourceID)
    
    if _, err := tx.ExecContext(ctx,
        "INSERT INTO users(id,name,email,source,source_id,created_at) VALUES($1,$2,$3,$4,$5,$6)",
        user.ID, user.Name, user.Email, user.Source, user.SourceID, time.Now(),
    ); err != nil {
        log.Printf("getOrCreateUser: ошибка создания пользователя: %v", err)
        return nil, err
    }
    
    log.Printf("getOrCreateUser: пользователь создан ID=%s", user.ID)
    return &user, nil
}