package adkagent

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"google.golang.org/adk/memory"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// ============================================================================
// CUSTOMER MEMORY SERVICE
// Запоминает предпочтения клиента между сессиями
// ============================================================================

// CustomerMemoryService реализует memory.Service для хранения предпочтений клиента
type CustomerMemoryService struct {
	mu       sync.RWMutex
	memories map[string][]memory.Entry // userID -> memories

	// TTL для памяти
	entryTTL time.Duration
}

// CustomerPreference структура для хранения предпочтений
type CustomerPreference struct {
	Type      string    // "category", "product", "language", "dietary"
	Value     string    // Значение предпочтения
	Count     int       // Сколько раз упоминалось
	LastSeen  time.Time // Последнее упоминание
	CreatedAt time.Time // Когда создано
}

// NewCustomerMemoryService создаёт сервис памяти клиента
func NewCustomerMemoryService() *CustomerMemoryService {
	cms := &CustomerMemoryService{
		memories: make(map[string][]memory.Entry),
		entryTTL: 30 * 24 * time.Hour, // 30 дней
	}

	// Запускаем очистку устаревших записей
	go cms.cleanupLoop()

	log.Printf("[MEMORY] ✅ Customer Memory Service инициализирован (TTL: %v)", cms.entryTTL)
	return cms
}

// AddSession добавляет сессию в память
// Извлекает полезную информацию из диалога и сохраняет как preferences
func (cms *CustomerMemoryService) AddSession(ctx context.Context, s session.Session) error {
	userID := s.UserID()
	if userID == "" {
		return nil // Анонимные пользователи не сохраняются
	}

	cms.mu.Lock()
	defer cms.mu.Unlock()

	// Извлекаем информацию из событий сессии
	events := s.Events()
	for i := 0; i < events.Len(); i++ {
		event := events.At(i)
		if event.LLMResponse.Content != nil {
			// Анализируем контент и извлекаем preferences
			cms.extractPreferences(userID, event)
		}
	}

	log.Printf("[MEMORY] Added session for user %s (total memories: %d)", userID, len(cms.memories[userID]))
	return nil
}

// extractPreferences извлекает предпочтения из события
func (cms *CustomerMemoryService) extractPreferences(userID string, event *session.Event) {
	if event == nil || event.LLMResponse.Content == nil {
		return
	}

	// Извлекаем текст из частей
	for _, part := range event.LLMResponse.Content.Parts {
		if part.Text != "" {
			// Создаём memory entry
			entry := memory.Entry{
				Content:   event.LLMResponse.Content,
				Author:    event.Author,
				Timestamp: event.Timestamp,
			}

			if cms.memories[userID] == nil {
				cms.memories[userID] = make([]memory.Entry, 0)
			}

			// Ограничиваем количество записей на пользователя
			if len(cms.memories[userID]) >= 100 {
				// Удаляем старые записи
				cms.memories[userID] = cms.memories[userID][1:]
			}

			cms.memories[userID] = append(cms.memories[userID], entry)
		}
	}
}

// Search ищет релевантные воспоминания по запросу
func (cms *CustomerMemoryService) Search(ctx context.Context, req *memory.SearchRequest) (*memory.SearchResponse, error) {
	if req == nil || req.UserID == "" {
		return &memory.SearchResponse{Memories: []memory.Entry{}}, nil
	}

	cms.mu.RLock()
	defer cms.mu.RUnlock()

	userMemories, exists := cms.memories[req.UserID]
	if !exists {
		return &memory.SearchResponse{Memories: []memory.Entry{}}, nil
	}

	// Простой поиск по ключевым словам (в продакшене - semantic search)
	var results []memory.Entry
	for _, mem := range userMemories {
		// Проверяем TTL
		if time.Since(mem.Timestamp) > cms.entryTTL {
			continue
		}

		// Добавляем в результаты (в продакшене здесь был бы semantic matching)
		results = append(results, mem)
	}

	// Ограничиваем количество результатов
	if len(results) > 10 {
		results = results[len(results)-10:]
	}

	log.Printf("[MEMORY] Search for user %s, query='%s': found %d memories", req.UserID, req.Query, len(results))
	return &memory.SearchResponse{Memories: results}, nil
}

// cleanupLoop периодически очищает устаревшие записи
func (cms *CustomerMemoryService) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		cms.cleanup()
	}
}

// cleanup удаляет устаревшие записи
func (cms *CustomerMemoryService) cleanup() {
	cms.mu.Lock()
	defer cms.mu.Unlock()

	now := time.Now()
	totalRemoved := 0

	for userID, memories := range cms.memories {
		var valid []memory.Entry
		for _, mem := range memories {
			if now.Sub(mem.Timestamp) <= cms.entryTTL {
				valid = append(valid, mem)
			} else {
				totalRemoved++
			}
		}

		if len(valid) == 0 {
			delete(cms.memories, userID)
		} else {
			cms.memories[userID] = valid
		}
	}

	if totalRemoved > 0 {
		log.Printf("[MEMORY] Cleanup: removed %d expired entries", totalRemoved)
	}
}

// GetUserPreferences возвращает сводку предпочтений пользователя
func (cms *CustomerMemoryService) GetUserPreferences(userID string) string {
	cms.mu.RLock()
	defer cms.mu.RUnlock()

	memories, exists := cms.memories[userID]
	if !exists || len(memories) == 0 {
		return ""
	}

	// Формируем краткую сводку для промпта
	summary := fmt.Sprintf("Customer history: %d interactions. ", len(memories))

	// Добавляем последнее взаимодействие
	if len(memories) > 0 {
		last := memories[len(memories)-1]
		if last.Content != nil && len(last.Content.Parts) > 0 {
			for _, part := range last.Content.Parts {
				if part.Text != "" {
					text := part.Text
					if len(text) > 100 {
						text = text[:100] + "..."
					}
					summary += fmt.Sprintf("Last topic: %s", text)
					break
				}
			}
		}
	}

	return summary
}

// AddPreference добавляет конкретное предпочтение пользователя
func (cms *CustomerMemoryService) AddPreference(userID, prefType, value string) {
	if userID == "" || prefType == "" || value == "" {
		return
	}

	cms.mu.Lock()
	defer cms.mu.Unlock()

	entry := memory.Entry{
		Content: genai.NewContentFromText(
			fmt.Sprintf("[PREFERENCE] %s: %s", prefType, value),
			genai.RoleUser,
		),
		Author:    "system",
		Timestamp: time.Now(),
	}

	if cms.memories[userID] == nil {
		cms.memories[userID] = make([]memory.Entry, 0)
	}

	cms.memories[userID] = append(cms.memories[userID], entry)
	log.Printf("[MEMORY] Added preference for user %s: %s=%s", userID, prefType, value)
}

// GetStats возвращает статистику по памяти
func (cms *CustomerMemoryService) GetStats() (totalUsers int, totalMemories int) {
	cms.mu.RLock()
	defer cms.mu.RUnlock()

	totalUsers = len(cms.memories)
	for _, mems := range cms.memories {
		totalMemories += len(mems)
	}

	return totalUsers, totalMemories
}
