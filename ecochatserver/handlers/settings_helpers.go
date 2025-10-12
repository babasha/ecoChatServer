package handlers

// maskAPIKey маскирует API ключ для безопасности
func maskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

// isMasked проверяет замаскирован ли API ключ
func isMasked(key string) bool {
	return key == "" || key == "****" || (len(key) > 4 && key[4:8] == "****")
}
