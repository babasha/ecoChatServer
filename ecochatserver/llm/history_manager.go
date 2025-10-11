package llm

import (
	"log"
)

// HistoryManager управляет размером истории диалога
type HistoryManager struct {
	maxMessages    int // Максимальное количество сообщений в истории
	maxTotalTokens int // Приблизительный лимит токенов (4 символа = 1 токен)
	systemTokens   int // Количество токенов в системном промпте
}

func NewHistoryManager() *HistoryManager {
	return &HistoryManager{
		maxMessages:    20,   // Храним последние 20 сообщений (10 пар вопрос-ответ)
		maxTotalTokens: 4000, // ~4000 токенов на историю (оставляем место для промпта)
		systemTokens:   2000, // ~2000 токенов занимает системный промпт
	}
}

// TrimHistory обрезает историю до разумного размера
func (hm *HistoryManager) TrimHistory(history []Message) []Message {
	if len(history) == 0 {
		return history
	}

	// Всегда сохраняем системное сообщение
	systemMsg := history[0]
	messages := history[1:]

	// 1. Ограничение по количеству сообщений
	if len(messages) > hm.maxMessages {
		// Берем только последние N сообщений
		messages = messages[len(messages)-hm.maxMessages:]
		log.Printf("[HistoryManager] Обрезано по количеству: оставлено %d сообщений", len(messages))
	}

	// 2. Ограничение по токенам (приблизительно)
	totalChars := 0
	for _, msg := range messages {
		totalChars += len(msg.Content)
	}

	// Приблизительно 4 символа = 1 токен для русского текста
	estimatedTokens := totalChars / 4

	if estimatedTokens > hm.maxTotalTokens {
		// Удаляем старые сообщения пока не влезем в лимит
		for estimatedTokens > hm.maxTotalTokens && len(messages) > 2 {
			removedMsg := messages[0]
			messages = messages[1:]
			estimatedTokens -= len(removedMsg.Content) / 4
		}
		log.Printf("[HistoryManager] Обрезано по токенам: оставлено %d сообщений (~%d токенов)",
			len(messages), estimatedTokens)
	}

	// Собираем обратно с системным сообщением
	result := make([]Message, 0, len(messages)+1)
	result = append(result, systemMsg)
	result = append(result, messages...)

	return result
}

// EstimateTokens приблизительно оценивает количество токенов
func (hm *HistoryManager) EstimateTokens(text string) int {
	// Для русского текста: ~4 символа = 1 токен
	// Для английского: ~4 символа = 1 токен
	return len(text) / 4
}

// GetStats возвращает статистику использования токенов
func (hm *HistoryManager) GetStats(history []Message) map[string]interface{} {
	if len(history) == 0 {
		return map[string]interface{}{
			"messages":        0,
			"estimatedTokens": 0,
			"systemTokens":    hm.systemTokens,
			"availableTokens": hm.maxTotalTokens,
			"utilizationPct":  0.0,
		}
	}

	totalChars := 0
	for i, msg := range history {
		if i == 0 {
			continue // Пропускаем системное сообщение
		}
		totalChars += len(msg.Content)
	}

	estimatedTokens := totalChars / 4
	utilization := float64(estimatedTokens) / float64(hm.maxTotalTokens) * 100

	return map[string]interface{}{
		"messages":        len(history) - 1, // Без системного сообщения
		"estimatedTokens": estimatedTokens,
		"systemTokens":    hm.systemTokens,
		"availableTokens": hm.maxTotalTokens - estimatedTokens,
		"utilizationPct":  utilization,
	}
}
