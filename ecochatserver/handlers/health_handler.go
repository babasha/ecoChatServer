package handlers

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/gin-gonic/gin"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
)

// HealthData представляет расширенные данные о здоровье системы
type HealthData struct {
	Timestamp string      `json:"timestamp"`
	Health    HealthState `json:"health"`
}

// HealthState представляет состояние здоровья системы
type HealthState struct {
	Overall    string         `json:"overall"` // "healthy" | "degraded" | "unhealthy"
	System     SystemMetrics  `json:"system"`
	Database   DatabaseHealth `json:"database"`
	Backend    BackendHealth  `json:"backend"`
	APILatency int            `json:"apiLatency"` // задержка API в мс
}

// SystemMetrics представляет системные метрики
type SystemMetrics struct {
	Memory MemoryInfo `json:"memory"`
	CPU    CPUInfo    `json:"cpu"`
	System SystemInfo `json:"system"`
}

// MemoryInfo представляет информацию о памяти
type MemoryInfo struct {
	Total        uint64  `json:"total"`
	Used         uint64  `json:"used"`
	Free         uint64  `json:"free"`
	UsagePercent float64 `json:"usagePercent"`
	TotalGB      string  `json:"totalGB"`
	UsedGB       string  `json:"usedGB"`
	FreeGB       string  `json:"freeGB"`
}

// CPUInfo представляет информацию о CPU
type CPUInfo struct {
	Count       int             `json:"count"`
	LoadAverage LoadAverageInfo `json:"loadAverage"`
	LoadPercent string          `json:"loadPercent"`
}

// LoadAverageInfo представляет среднюю загрузку CPU
type LoadAverageInfo struct {
	OneMin     float64 `json:"1min"`
	FiveMin    float64 `json:"5min"`
	FifteenMin float64 `json:"15min"`
}

// SystemInfo представляет общую информацию о системе
type SystemInfo struct {
	Platform        string `json:"platform"`
	Uptime          uint64 `json:"uptime"`
	UptimeFormatted string `json:"uptimeFormatted"`
	Hostname        string `json:"hostname"`
}

// DatabaseHealth представляет здоровье базы данных
type DatabaseHealth struct {
	Healthy     bool `json:"healthy"`
	Latency     int  `json:"latency"` // в мс
	Connections int  `json:"connections"`
}

// BackendHealth представляет здоровье backend сервера
type BackendHealth struct {
	Healthy bool `json:"healthy"`
	Latency int  `json:"latency"` // в мс
}

// GetHealthData возвращает расширенные данные о здоровье системы
func GetHealthData(c *gin.Context) {
	startTime := time.Now()

	healthData := HealthData{
		Timestamp: time.Now().Format(time.RFC3339),
	}

	// 1. Системные метрики
	systemMetrics, err := collectSystemMetrics()
	if err != nil {
		log.Printf("GetHealthData: ошибка сбора системных метрик: %v", err)
	}
	healthData.Health.System = systemMetrics

	// 2. Здоровье базы данных
	dbHealth := checkDatabaseHealth()
	healthData.Health.Database = dbHealth

	// 3. Здоровье backend (сам сервер)
	backendHealth := BackendHealth{
		Healthy: true,
		Latency: int(time.Since(startTime).Milliseconds()),
	}
	healthData.Health.Backend = backendHealth

	// 4. API latency
	healthData.Health.APILatency = int(time.Since(startTime).Milliseconds())

	// 5. Определяем общий статус здоровья
	healthData.Health.Overall = determineOverallHealth(systemMetrics, dbHealth, backendHealth)

	c.JSON(http.StatusOK, healthData)
}

// collectSystemMetrics собирает системные метрики
func collectSystemMetrics() (SystemMetrics, error) {
	metrics := SystemMetrics{}

	// Память
	vmStat, err := mem.VirtualMemory()
	if err != nil {
		log.Printf("collectSystemMetrics: ошибка получения информации о памяти: %v", err)
	} else {
		metrics.Memory = MemoryInfo{
			Total:        vmStat.Total,
			Used:         vmStat.Used,
			Free:         vmStat.Free,
			UsagePercent: vmStat.UsedPercent,
			TotalGB:      fmt.Sprintf("%.2f", float64(vmStat.Total)/1024/1024/1024),
			UsedGB:       fmt.Sprintf("%.2f", float64(vmStat.Used)/1024/1024/1024),
			FreeGB:       fmt.Sprintf("%.2f", float64(vmStat.Free)/1024/1024/1024),
		}
	}

	// CPU
	cpuCount := runtime.NumCPU()
	loadAvg, err := load.Avg()
	if err != nil {
		log.Printf("collectSystemMetrics: ошибка получения загрузки CPU: %v", err)
		loadAvg = &load.AvgStat{Load1: 0, Load5: 0, Load15: 0}
	}

	loadPercent := (loadAvg.Load1 / float64(cpuCount)) * 100
	if loadPercent > 100 {
		loadPercent = 100
	}

	metrics.CPU = CPUInfo{
		Count: cpuCount,
		LoadAverage: LoadAverageInfo{
			OneMin:     loadAvg.Load1,
			FiveMin:    loadAvg.Load5,
			FifteenMin: loadAvg.Load15,
		},
		LoadPercent: fmt.Sprintf("%.1f", loadPercent),
	}

	// Информация о системе
	hostInfo, err := host.Info()
	if err != nil {
		log.Printf("collectSystemMetrics: ошибка получения информации о хосте: %v", err)
	} else {
		metrics.System = SystemInfo{
			Platform:        hostInfo.Platform,
			Uptime:          hostInfo.Uptime,
			UptimeFormatted: formatUptime(hostInfo.Uptime),
			Hostname:        hostInfo.Hostname,
		}
	}

	// Fallback для hostname
	if metrics.System.Hostname == "" {
		if hostname, err := os.Hostname(); err == nil {
			metrics.System.Hostname = hostname
		}
	}

	return metrics, nil
}

// checkDatabaseHealth проверяет здоровье базы данных
func checkDatabaseHealth() DatabaseHealth {
	health := DatabaseHealth{
		Healthy: false,
		Latency: 0,
	}

	if database.DB == nil {
		return health
	}

	// Проверяем подключение и измеряем задержку
	start := time.Now()
	err := database.DB.Ping()
	latency := time.Since(start).Milliseconds()

	health.Latency = int(latency)

	if err != nil {
		log.Printf("checkDatabaseHealth: ошибка подключения к БД: %v", err)
		return health
	}

	health.Healthy = true

	// Получаем количество активных подключений
	var connections int
	err = database.DB.QueryRow("SELECT COUNT(*) FROM pg_stat_activity").Scan(&connections)
	if err != nil && err != sql.ErrNoRows {
		log.Printf("checkDatabaseHealth: ошибка получения количества подключений: %v", err)
	} else {
		health.Connections = connections
	}

	return health
}

// determineOverallHealth определяет общий статус здоровья системы
func determineOverallHealth(system SystemMetrics, db DatabaseHealth, backend BackendHealth) string {
	// Проверяем критические параметры
	if !db.Healthy {
		return "unhealthy"
	}

	if !backend.Healthy {
		return "unhealthy"
	}

	// Проверяем параметры производительности
	if system.Memory.UsagePercent > 90 {
		return "unhealthy"
	}

	if system.Memory.UsagePercent > 75 {
		return "degraded"
	}

	// Проверяем загрузку CPU
	cpuLoad := system.CPU.LoadAverage.OneMin / float64(system.CPU.Count)
	if cpuLoad > 2.0 {
		return "unhealthy"
	}

	if cpuLoad > 1.5 {
		return "degraded"
	}

	// Проверяем задержки
	if db.Latency > 1000 || backend.Latency > 1000 {
		return "unhealthy"
	}

	if db.Latency > 500 || backend.Latency > 500 {
		return "degraded"
	}

	return "healthy"
}

// formatUptime форматирует uptime в читаемый формат
func formatUptime(seconds uint64) string {
	days := seconds / 86400
	hours := (seconds % 86400) / 3600
	minutes := (seconds % 3600) / 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}
