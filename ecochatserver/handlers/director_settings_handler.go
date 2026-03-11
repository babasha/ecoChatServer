package handlers

import (
	"log"
	"net/http"
	"strconv"

	"github.com/egor/ecochatserver/database"
	"github.com/gin-gonic/gin"
)

// DirectorSettingsResponse is the shape of director settings returned to the admin panel.
type DirectorSettingsResponse struct {
	// Digest retention (days)
	DigestRetentionWeekly    int `json:"digest_retention_weekly"`
	DigestRetentionMonthly   int `json:"digest_retention_monthly"`
	DigestRetentionQuarterly int `json:"digest_retention_quarterly"`

	// Memory settings
	MemoryDecayHalfLifeDays int  `json:"memory_decay_halflife_days"`
	MemoryMaxPerCategory    int  `json:"memory_max_per_category"`
	MemoryAutoCleanup       bool `json:"memory_auto_cleanup"`

	// Analysis settings
	AnalysisIntervalHours      int  `json:"analysis_interval_hours"`
	AnalysisAutoApplyDirectives bool `json:"analysis_auto_apply_directives"`
	AnalysisMaxDirectives       int  `json:"analysis_max_directives"`

	// Archive settings
	ChatArchiveAfterDays int `json:"chat_archive_after_days"`
	ReportRetentionDays  int `json:"report_retention_days"`

	// Escalation thresholds
	EscalationThresholdPercent  int `json:"escalation_threshold_percent"`
	EmptyResponseThresholdPercent int `json:"empty_response_threshold_percent"`
}

// Setting keys in app_settings table
const (
	skDigestRetWeekly    = "DIRECTOR_DIGEST_RETENTION_WEEKLY"
	skDigestRetMonthly   = "DIRECTOR_DIGEST_RETENTION_MONTHLY"
	skDigestRetQuarterly = "DIRECTOR_DIGEST_RETENTION_QUARTERLY"
	skDecayHalfLife      = "DIRECTOR_MEMORY_DECAY_HALFLIFE"
	skMaxPerCategory     = "DIRECTOR_MEMORY_MAX_PER_CATEGORY"
	skAutoCleanup        = "DIRECTOR_MEMORY_AUTO_CLEANUP"
	skAnalysisInterval   = "DIRECTOR_ANALYSIS_INTERVAL_HOURS"
	skAutoApply          = "DIRECTOR_AUTO_APPLY_DIRECTIVES"
	skMaxDirectives      = "DIRECTOR_MAX_DIRECTIVES"
	skChatArchiveDays    = "DIRECTOR_CHAT_ARCHIVE_DAYS"
	skReportRetention    = "DIRECTOR_REPORT_RETENTION_DAYS"
	skEscThreshold       = "DIRECTOR_ESCALATION_THRESHOLD"
	skEmptyThreshold     = "DIRECTOR_EMPTY_THRESHOLD"
)

// GetDirectorSettings returns all Director configuration values.
func GetDirectorSettings(c *gin.Context) {
	settings := DirectorSettingsResponse{
		DigestRetentionWeekly:         database.GetSettingInt(skDigestRetWeekly, 90),
		DigestRetentionMonthly:        database.GetSettingInt(skDigestRetMonthly, 365),
		DigestRetentionQuarterly:      database.GetSettingInt(skDigestRetQuarterly, 730),
		MemoryDecayHalfLifeDays:       database.GetSettingInt(skDecayHalfLife, 30),
		MemoryMaxPerCategory:          database.GetSettingInt(skMaxPerCategory, 100),
		MemoryAutoCleanup:             database.GetSettingBool(skAutoCleanup, true),
		AnalysisIntervalHours:         database.GetSettingInt(skAnalysisInterval, 6),
		AnalysisAutoApplyDirectives:   database.GetSettingBool(skAutoApply, false),
		AnalysisMaxDirectives:         database.GetSettingInt(skMaxDirectives, 5),
		ChatArchiveAfterDays:          database.GetSettingInt(skChatArchiveDays, 30),
		ReportRetentionDays:           database.GetSettingInt(skReportRetention, 180),
		EscalationThresholdPercent:    database.GetSettingInt(skEscThreshold, 10),
		EmptyResponseThresholdPercent: database.GetSettingInt(skEmptyThreshold, 5),
	}

	c.JSON(http.StatusOK, settings)
}

// UpdateDirectorSettings saves Director configuration values.
func UpdateDirectorSettings(c *gin.Context) {
	var req DirectorSettingsResponse
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	// Validate ranges
	if req.DigestRetentionWeekly < 7 {
		req.DigestRetentionWeekly = 7
	}
	if req.DigestRetentionMonthly < 30 {
		req.DigestRetentionMonthly = 30
	}
	if req.DigestRetentionQuarterly < 90 {
		req.DigestRetentionQuarterly = 90
	}
	if req.MemoryDecayHalfLifeDays < 7 {
		req.MemoryDecayHalfLifeDays = 7
	}
	if req.MemoryMaxPerCategory < 10 {
		req.MemoryMaxPerCategory = 10
	}
	if req.AnalysisIntervalHours < 1 {
		req.AnalysisIntervalHours = 1
	}
	if req.AnalysisMaxDirectives < 1 {
		req.AnalysisMaxDirectives = 1
	}
	if req.AnalysisMaxDirectives > 20 {
		req.AnalysisMaxDirectives = 20
	}
	if req.ChatArchiveAfterDays < 1 {
		req.ChatArchiveAfterDays = 1
	}
	if req.ReportRetentionDays < 7 {
		req.ReportRetentionDays = 7
	}
	if req.EscalationThresholdPercent < 1 {
		req.EscalationThresholdPercent = 1
	}
	if req.EmptyResponseThresholdPercent < 1 {
		req.EmptyResponseThresholdPercent = 1
	}

	// Save all settings
	pairs := []struct {
		key   string
		value string
		desc  string
	}{
		{skDigestRetWeekly, strconv.Itoa(req.DigestRetentionWeekly), "Weekly digest retention in days"},
		{skDigestRetMonthly, strconv.Itoa(req.DigestRetentionMonthly), "Monthly digest retention in days"},
		{skDigestRetQuarterly, strconv.Itoa(req.DigestRetentionQuarterly), "Quarterly digest retention in days"},
		{skDecayHalfLife, strconv.Itoa(req.MemoryDecayHalfLifeDays), "Memory decay half-life in days"},
		{skMaxPerCategory, strconv.Itoa(req.MemoryMaxPerCategory), "Max memories per category"},
		{skAutoCleanup, strconv.FormatBool(req.MemoryAutoCleanup), "Auto-cleanup expired memories"},
		{skAnalysisInterval, strconv.Itoa(req.AnalysisIntervalHours), "Analysis interval in hours"},
		{skAutoApply, strconv.FormatBool(req.AnalysisAutoApplyDirectives), "Auto-apply directives"},
		{skMaxDirectives, strconv.Itoa(req.AnalysisMaxDirectives), "Max directives per report"},
		{skChatArchiveDays, strconv.Itoa(req.ChatArchiveAfterDays), "Archive chats after N days"},
		{skReportRetention, strconv.Itoa(req.ReportRetentionDays), "Report retention in days"},
		{skEscThreshold, strconv.Itoa(req.EscalationThresholdPercent), "Escalation rate threshold %"},
		{skEmptyThreshold, strconv.Itoa(req.EmptyResponseThresholdPercent), "Empty response threshold %"},
	}

	for _, p := range pairs {
		if err := database.SetSetting(p.key, p.value, p.desc); err != nil {
			log.Printf("[DIRECTOR_SETTINGS] Error saving %s: %v", p.key, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save setting: " + p.key})
			return
		}
	}

	log.Printf("[DIRECTOR_SETTINGS] Settings updated by admin")
	c.JSON(http.StatusOK, gin.H{"status": "saved"})
}
