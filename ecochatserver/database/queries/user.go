package queries

import (
	"context"
	"database/sql"
	"log"
	"time"

	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

func getOrCreateUser(
	ctx context.Context, tx *sql.Tx,
	userID, userName, userEmail, source, sourceID string,
) (*models.User, error) {
	log.Printf("getOrCreateUser: начало, userID=%s, userName=%s, source=%s, sourceID=%s",
		userID, userName, source, sourceID)

	var user models.User
	var avatarNull, profileURLNull sql.NullString

	err := tx.QueryRowContext(ctx,
		"SELECT id,name,email,avatar,profile_url,source,source_id FROM users WHERE source=$1 AND source_id=$2 LIMIT 1",
		source, sourceID,
	).Scan(&user.ID, &user.Name, &user.Email, &avatarNull, &profileURLNull, &user.Source, &user.SourceID)

	if err != nil && err != sql.ErrNoRows {
		log.Printf("getOrCreateUser: ошибка поиска пользователя: %v", err)
		return nil, err
	}

	if err == nil {
		user.Avatar = nullStringToPointer(avatarNull)
		user.ProfileURL = nullStringToPointer(profileURLNull)
		// Обновляем имя, если передано новое непустое имя, отличное от текущего
		if userName != "" && userName != user.Name {
			log.Printf("getOrCreateUser: обновляем имя пользователя ID=%s: %q -> %q", user.ID, user.Name, userName)
			if _, err := tx.ExecContext(ctx,
				"UPDATE users SET name=$1 WHERE id=$2",
				userName, user.ID,
			); err != nil {
				log.Printf("getOrCreateUser: ошибка обновления имени: %v", err)
			} else {
				user.Name = userName
			}
		}
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

// UpdateUserProfile обновляет avatar и profile_url пользователя
func UpdateUserProfile(db *sql.DB, userID uuid.UUID, avatar, profileURL string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := db.ExecContext(ctx,
		"UPDATE users SET avatar=COALESCE(NULLIF($1,''),avatar), profile_url=COALESCE(NULLIF($2,''),profile_url) WHERE id=$3",
		avatar, profileURL, userID,
	)
	if err != nil {
		log.Printf("UpdateUserProfile: ошибка обновления профиля ID=%s: %v", userID, err)
		return err
	}
	log.Printf("UpdateUserProfile: профиль обновлен ID=%s, avatar=%v, profileURL=%v",
		userID, avatar != "", profileURL != "")
	return nil
}
