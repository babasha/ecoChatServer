package database

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var (
	RedisClient *redis.Client
	redisCtx    = context.Background()
)

// InitRedis инициализирует Redis клиент
func InitRedis() error {
	// Получаем настройки из переменных окружения или app_settings
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		// Пробуем загрузить из app_settings если БД уже инициализирована
		if DB != nil {
			redisURL = GetSetting("REDIS_URL", "")
		}
	}

	// Если Redis URL не настроен - работаем без кеша
	if redisURL == "" {
		log.Println("[REDIS] Redis URL не настроен, кеширование отключено")
		return nil
	}

	// Парсим Redis URL
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return fmt.Errorf("ошибка парсинга REDIS_URL: %w", err)
	}

	// Создаем клиент
	RedisClient = redis.NewClient(opt)

	// Проверяем подключение
	ctx, cancel := context.WithTimeout(redisCtx, 5*time.Second)
	defer cancel()

	if err := RedisClient.Ping(ctx).Err(); err != nil {
		RedisClient = nil
		return fmt.Errorf("не удалось подключиться к Redis: %w", err)
	}

	log.Printf("[REDIS] ✅ Успешное подключение к Redis")
	return nil
}

// CloseRedis закрывает соединение с Redis
func CloseRedis() {
	if RedisClient != nil {
		if err := RedisClient.Close(); err != nil {
			log.Printf("[REDIS] Ошибка закрытия соединения: %v", err)
		} else {
			log.Println("[REDIS] Соединение закрыто")
		}
	}
}

// Константы для Redis ключей
const (
	chatsCachePrefix = "chats:active:"   // Префикс для кеша активных чатов
	chatCacheTTL     = 2 * time.Minute   // TTL для кеша списка чатов
)

// chatsCacheKey генерирует ключ для кеша чатов
func chatsCacheKey(clientID, adminID uuid.UUID, page, size int) string {
	return fmt.Sprintf("%s%s:%s:%d:%d", chatsCachePrefix, clientID, adminID, page, size)
}

// CacheChatsResponse структура для кеширования ответа GetChats
type CacheChatsResponse struct {
	Chats []models.ChatResponse `json:"chats"`
	Total int                   `json:"total"`
}

// GetCachedChats получает список чатов из Redis кеша
func GetCachedChats(clientID, adminID uuid.UUID, page, size int) ([]models.ChatResponse, int, bool) {
	if RedisClient == nil {
		return nil, 0, false
	}

	key := chatsCacheKey(clientID, adminID, page, size)
	ctx, cancel := context.WithTimeout(redisCtx, 500*time.Millisecond)
	defer cancel()

	data, err := RedisClient.Get(ctx, key).Bytes()
	if err != nil {
		if err != redis.Nil {
			log.Printf("[REDIS] Ошибка чтения кеша чатов: %v", err)
		}
		return nil, 0, false
	}

	var cached CacheChatsResponse
	if err := json.Unmarshal(data, &cached); err != nil {
		log.Printf("[REDIS] Ошибка десериализации кеша чатов: %v", err)
		return nil, 0, false
	}

	log.Printf("[REDIS] ✓ Получены чаты из кеша: clientID=%s, adminID=%s, page=%d, count=%d",
		clientID, adminID, page, len(cached.Chats))
	return cached.Chats, cached.Total, true
}

// SetCachedChats сохраняет список чатов в Redis кеш
func SetCachedChats(clientID, adminID uuid.UUID, page, size int, chats []models.ChatResponse, total int) {
	if RedisClient == nil {
		return
	}

	key := chatsCacheKey(clientID, adminID, page, size)
	cached := CacheChatsResponse{
		Chats: chats,
		Total: total,
	}

	data, err := json.Marshal(cached)
	if err != nil {
		log.Printf("[REDIS] Ошибка сериализации чатов для кеша: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(redisCtx, 500*time.Millisecond)
	defer cancel()

	if err := RedisClient.Set(ctx, key, data, chatCacheTTL).Err(); err != nil {
		log.Printf("[REDIS] Ошибка сохранения чатов в кеш: %v", err)
		return
	}

	log.Printf("[REDIS] ✓ Чаты сохранены в кеш: clientID=%s, adminID=%s, page=%d, count=%d, ttl=%v",
		clientID, adminID, page, len(chats), chatCacheTTL)
}

// InvalidateChatsCacheForAll инвалидирует весь кеш чатов (при архивации/создании/обновлении)
func InvalidateChatsCacheForAll() {
	if RedisClient == nil {
		return
	}

	ctx, cancel := context.WithTimeout(redisCtx, 1*time.Second)
	defer cancel()

	// Удаляем все ключи с префиксом chats:active:*
	pattern := chatsCachePrefix + "*"
	iter := RedisClient.Scan(ctx, 0, pattern, 0).Iterator()

	deletedCount := 0
	for iter.Next(ctx) {
		key := iter.Val()
		if err := RedisClient.Del(ctx, key).Err(); err != nil {
			log.Printf("[REDIS] Ошибка удаления ключа %s: %v", key, err)
		} else {
			deletedCount++
		}
	}

	if err := iter.Err(); err != nil {
		log.Printf("[REDIS] Ошибка сканирования ключей для инвалидации: %v", err)
		return
	}

	log.Printf("[REDIS] ✓ Инвалидирован кеш чатов: удалено %d ключей", deletedCount)
}
