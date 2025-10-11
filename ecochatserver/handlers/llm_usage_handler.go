package handlers

import (
	"net/http"
	"time"

	"github.com/egor/ecochatserver/llm"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GetLLMUsageStats возвращает статистику использования LLM токенов
func GetLLMUsageStats(c *gin.Context) {
	// Параметры для фильтрации
	startDateStr := c.DefaultQuery("start_date", time.Now().AddDate(0, 0, -30).Format("2006-01-02"))
	endDateStr := c.DefaultQuery("end_date", time.Now().Format("2006-01-02"))
	clientIDStr := c.Query("client_id")

	// Парсим даты
	startDate, err := time.Parse("2006-01-02", startDateStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid start_date format, use YYYY-MM-DD",
		})
		return
	}

	endDate, err := time.Parse("2006-01-02", endDateStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid end_date format, use YYYY-MM-DD",
		})
		return
	}

	// Парсим client_id если указан
	var clientID *uuid.UUID
	if clientIDStr != "" {
		parsedID, err := uuid.Parse(clientIDStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "Invalid client_id format",
			})
			return
		}
		clientID = &parsedID
	}

	// Получаем статистику
	stats, err := llm.GetDailyStats(c.Request.Context(), startDate, endDate, clientID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to get LLM usage stats",
			"details": err.Error(),
		})
		return
	}

	// Получаем оценки стоимости
	costs, err := llm.GetCostEstimates(c.Request.Context(), startDate, endDate, clientID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to get cost estimates",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"stats":    stats,
			"costs":    costs,
			"period": gin.H{
				"start": startDateStr,
				"end":   endDateStr,
			},
		},
	})
}

// GetLLMUsageSummary возвращает краткую сводку использования
func GetLLMUsageSummary(c *gin.Context) {
	// Получаем статистику за последние 30 дней
	endDate := time.Now()
	startDate := endDate.AddDate(0, 0, -30)

	stats, err := llm.GetDailyStats(c.Request.Context(), startDate, endDate, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to get stats",
		})
		return
	}

	costs, err := llm.GetCostEstimates(c.Request.Context(), startDate, endDate, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to get costs",
		})
		return
	}

	// Подсчитываем итоги
	var (
		totalRequests      int64
		totalTokens        int64
		totalCost          float64
		providerBreakdown  = make(map[string]int64)
	)

	for _, stat := range stats {
		if count, ok := stat["request_count"].(int64); ok {
			totalRequests += count
		}
		if tokens, ok := stat["total_tokens"].(int64); ok {
			totalTokens += tokens
		}
		if provider, ok := stat["provider"].(string); ok {
			if tokens, ok := stat["total_tokens"].(int64); ok {
				providerBreakdown[provider] += tokens
			}
		}
	}

	for _, cost := range costs {
		if c, ok := cost["estimated_cost_usd"].(float64); ok {
			totalCost += c
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"summary": gin.H{
			"total_requests":      totalRequests,
			"total_tokens":        totalTokens,
			"total_cost_usd":      totalCost,
			"provider_breakdown":  providerBreakdown,
			"period_days":         30,
		},
	})
}
