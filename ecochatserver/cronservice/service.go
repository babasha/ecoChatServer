package cronservice

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

// ActionHandler is called when a cron job fires.
// Returns an error string (empty on success).
type ActionHandler func(ctx context.Context, job *models.CronJob) error

// Service manages scheduled tasks with persistence.
type Service struct {
	mu       sync.RWMutex
	cron     *cron.Cron
	entries  map[uuid.UUID]cron.EntryID // jobID → cron entry
	handlers map[string]ActionHandler   // action name → handler
	stopCh   chan struct{}
	running  bool
}

// New creates a new CronService.
func New() *Service {
	return &Service{
		entries:  make(map[uuid.UUID]cron.EntryID),
		handlers: make(map[string]ActionHandler),
	}
}

// RegisterAction registers a handler for a named action.
func (s *Service) RegisterAction(name string, handler ActionHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[name] = handler
}

// Start loads jobs from DB and begins the scheduler.
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return nil
	}

	s.cron = cron.New(cron.WithSeconds(), cron.WithLocation(time.UTC))
	s.stopCh = make(chan struct{})

	// Load persisted jobs
	jobs, err := database.GetEnabledCronJobs()
	if err != nil {
		log.Printf("[CRON] Warning: failed to load jobs from DB: %v", err)
	} else {
		for i := range jobs {
			if err := s.scheduleJobLocked(&jobs[i]); err != nil {
				log.Printf("[CRON] Failed to schedule job %s (%s): %v", jobs[i].Name, jobs[i].ID, err)
			}
		}
		log.Printf("[CRON] Loaded %d jobs from DB", len(jobs))
	}

	s.cron.Start()
	s.running = true

	// Background: check for due "at" jobs and cleanup
	go s.maintenanceLoop(ctx)

	log.Println("[CRON] Service started")
	return nil
}

// Stop gracefully shuts down the scheduler.
func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return
	}

	s.cron.Stop()
	close(s.stopCh)
	s.running = false
	log.Println("[CRON] Service stopped")
}

// AddJob creates and schedules a new job.
func (s *Service) AddJob(req *models.CronJobRequest, createdBy string) (*models.CronJob, error) {
	// Validate schedule
	nextRun, err := computeNextRun(req.ScheduleType, req.Schedule, req.Timezone, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid schedule: %w", err)
	}

	// Check name uniqueness
	existing, _ := database.GetCronJobByName(req.Name)
	if existing != nil {
		return nil, fmt.Errorf("job with name %q already exists", req.Name)
	}

	action := req.Action
	if action == "" {
		action = "analyze"
	}

	maxRuns := req.MaxRuns
	if req.ScheduleType == "at" && maxRuns == 0 {
		maxRuns = 1 // one-shot by default for "at" type
	}

	now := time.Now()
	job := &models.CronJob{
		ID:           uuid.New(),
		Name:         req.Name,
		Description:  req.Description,
		ScheduleType: req.ScheduleType,
		Schedule:     req.Schedule,
		Timezone:     req.Timezone,
		Action:       action,
		ActionConfig: req.ActionConfig,
		Enabled:      true,
		CreatedBy:    createdBy,
		NextRunAt:    nextRun,
		MaxRuns:      maxRuns,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := database.InsertCronJob(job); err != nil {
		return nil, fmt.Errorf("save job: %w", err)
	}

	s.mu.Lock()
	if s.running {
		if err := s.scheduleJobLocked(job); err != nil {
			s.mu.Unlock()
			return job, fmt.Errorf("saved but failed to schedule: %w", err)
		}
	}
	s.mu.Unlock()

	log.Printf("[CRON] Job added: %s (type=%s, schedule=%s, action=%s)", job.Name, job.ScheduleType, job.Schedule, job.Action)
	return job, nil
}

// RemoveJob removes a job by ID.
func (s *Service) RemoveJob(id uuid.UUID) error {
	s.mu.Lock()
	if entryID, ok := s.entries[id]; ok {
		s.cron.Remove(entryID)
		delete(s.entries, id)
	}
	s.mu.Unlock()

	return database.DeleteCronJob(id)
}

// ToggleJob enables or disables a job.
func (s *Service) ToggleJob(id uuid.UUID, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if enabled {
		// Re-schedule
		job, err := database.GetCronJob(id)
		if err != nil || job == nil {
			return fmt.Errorf("job not found")
		}

		en := true
		if err := database.UpdateCronJob(id, nil, nil, nil, nil, &en); err != nil {
			return err
		}

		if s.running {
			return s.scheduleJobLocked(job)
		}
	} else {
		// Remove from scheduler
		if entryID, ok := s.entries[id]; ok {
			s.cron.Remove(entryID)
			delete(s.entries, id)
		}
		return database.DisableCronJob(id)
	}
	return nil
}

// RunNow manually triggers a job immediately (outside of schedule).
func (s *Service) RunNow(id uuid.UUID) error {
	job, err := database.GetCronJob(id)
	if err != nil || job == nil {
		return fmt.Errorf("job not found")
	}

	go s.executeJob(job)
	return nil
}

// ListJobs returns all jobs.
func (s *Service) ListJobs() ([]models.CronJob, error) {
	return database.ListCronJobs()
}

// ============================================================================
// Internal scheduling
// ============================================================================

// scheduleJobLocked adds a job to the cron scheduler. Must be called with s.mu held.
func (s *Service) scheduleJobLocked(job *models.CronJob) error {
	// Remove existing entry if re-scheduling
	if entryID, ok := s.entries[job.ID]; ok {
		s.cron.Remove(entryID)
		delete(s.entries, job.ID)
	}

	jobCopy := *job // capture for closure

	switch job.ScheduleType {
	case "cron":
		// Cron expression with optional timezone
		spec := job.Schedule
		if job.Timezone != "" {
			spec = fmt.Sprintf("CRON_TZ=%s %s", job.Timezone, spec)
		}
		// robfig/cron v3 with seconds: expects 6 fields
		// If user provides 5-field expr, prepend "0" for seconds
		fields := countFields(spec)
		if fields == 5 || (job.Timezone != "" && fields == 6) {
			// 5-field standard cron (or 5+TZ prefix) → prepend seconds=0
			if job.Timezone != "" {
				spec = fmt.Sprintf("CRON_TZ=%s 0 %s", job.Timezone, job.Schedule)
			} else {
				spec = "0 " + spec
			}
		}

		entryID, err := s.cron.AddFunc(spec, func() { s.executeJob(&jobCopy) })
		if err != nil {
			return fmt.Errorf("invalid cron expression %q: %w", spec, err)
		}
		s.entries[job.ID] = entryID

	case "every":
		// Duration-based interval
		dur, err := parseDuration(job.Schedule)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", job.Schedule, err)
		}
		entryID, err := s.cron.AddFunc(fmt.Sprintf("@every %s", dur.String()), func() { s.executeJob(&jobCopy) })
		if err != nil {
			return fmt.Errorf("schedule every %s: %w", dur, err)
		}
		s.entries[job.ID] = entryID

	case "at":
		// One-shot — handled by maintenance loop polling, not by cron scheduler.
		// We just update nextRunAt and let the loop pick it up.
		return nil

	default:
		return fmt.Errorf("unknown schedule type: %s", job.ScheduleType)
	}

	return nil
}

// executeJob runs a job's action and logs the result.
func (s *Service) executeJob(job *models.CronJob) {
	start := time.Now()

	log.Printf("[CRON] Executing job: %s (action=%s)", job.Name, job.Action)

	s.mu.RLock()
	handler, ok := s.handlers[job.Action]
	s.mu.RUnlock()

	var jobErr error
	if !ok {
		jobErr = fmt.Errorf("no handler registered for action %q", job.Action)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		jobErr = handler(ctx, job)
	}

	duration := time.Since(start)
	status := "success"
	errMsg := ""
	if jobErr != nil {
		status = "failed"
		errMsg = jobErr.Error()
		log.Printf("[CRON] Job %s failed: %v (took %v)", job.Name, jobErr, duration)
	} else {
		log.Printf("[CRON] Job %s completed (took %v)", job.Name, duration)
	}

	// Log run
	runLog := &models.CronRunLog{
		ID:        uuid.New(),
		JobID:     job.ID,
		Status:    status,
		Duration:  int(duration.Milliseconds()),
		Error:     errMsg,
		CreatedAt: time.Now(),
	}
	if err := database.InsertCronRunLog(runLog); err != nil {
		log.Printf("[CRON] Failed to log run: %v", err)
	}

	// Compute next run
	nextRun, _ := computeNextRun(job.ScheduleType, job.Schedule, job.Timezone, &start)

	// Update job in DB
	if err := database.UpdateCronJobRun(job.ID, errMsg, nextRun); err != nil {
		log.Printf("[CRON] Failed to update job run: %v", err)
	}

	// Check max_runs
	freshJob, _ := database.GetCronJob(job.ID)
	if freshJob != nil && freshJob.MaxRuns > 0 && freshJob.RunCount >= freshJob.MaxRuns {
		log.Printf("[CRON] Job %s reached max_runs=%d, disabling", job.Name, freshJob.MaxRuns)
		s.ToggleJob(job.ID, false)
	}
}

// maintenanceLoop checks for "at" jobs and performs cleanup.
func (s *Service) maintenanceLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	cleanupTicker := time.NewTicker(24 * time.Hour)
	defer cleanupTicker.Stop()

	for {
		select {
		case <-ticker.C:
			s.checkAtJobs()
		case <-cleanupTicker.C:
			s.cleanupRunLogs()
		case <-s.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// checkAtJobs polls for "at" type jobs that are due.
func (s *Service) checkAtJobs() {
	jobs, err := database.GetEnabledCronJobs()
	if err != nil {
		return
	}

	now := time.Now()
	for i := range jobs {
		job := &jobs[i]
		if job.ScheduleType != "at" {
			continue
		}
		if job.NextRunAt != nil && now.After(*job.NextRunAt) {
			go s.executeJob(job)
		}
	}
}

// cleanupRunLogs removes old run log entries.
func (s *Service) cleanupRunLogs() {
	retention := database.GetSettingInt("CRON_RUN_LOG_RETENTION_DAYS", 30)
	if deleted, err := database.CleanupOldCronRunLogs(retention); err != nil {
		log.Printf("[CRON] Run log cleanup error: %v", err)
	} else if deleted > 0 {
		log.Printf("[CRON] Cleaned up %d old run logs", deleted)
	}
}

// ============================================================================
// Schedule computation helpers
// ============================================================================

// computeNextRun calculates when a job should run next.
func computeNextRun(scheduleType, schedule, timezone string, after *time.Time) (*time.Time, error) {
	now := time.Now()
	if after != nil {
		now = *after
	}

	switch scheduleType {
	case "at":
		t, err := parseAtTime(schedule, timezone)
		if err != nil {
			return nil, err
		}
		return &t, nil

	case "every":
		dur, err := parseDuration(schedule)
		if err != nil {
			return nil, err
		}
		next := now.Add(dur)
		return &next, nil

	case "cron":
		spec := schedule
		if timezone != "" {
			spec = fmt.Sprintf("CRON_TZ=%s %s", timezone, schedule)
		}
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		sched, err := parser.Parse(spec)
		if err != nil {
			return nil, fmt.Errorf("invalid cron expr: %w", err)
		}
		next := sched.Next(now)
		return &next, nil

	default:
		return nil, fmt.Errorf("unknown schedule type: %s", scheduleType)
	}
}

// parseAtTime parses an ISO datetime or relative time string.
func parseAtTime(s, timezone string) (time.Time, error) {
	// Try ISO 8601 formats
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	} {
		t, err := time.Parse(layout, s)
		if err == nil {
			if timezone != "" {
				loc, locErr := time.LoadLocation(timezone)
				if locErr == nil {
					t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, loc)
				}
			}
			return t, nil
		}
	}

	// Try relative duration (e.g., "in 2h", "30m")
	cleaned := s
	if len(cleaned) > 3 && cleaned[:3] == "in " {
		cleaned = cleaned[3:]
	}
	dur, err := parseDuration(cleaned)
	if err != nil {
		return time.Time{}, fmt.Errorf("cannot parse time %q", s)
	}
	return time.Now().Add(dur), nil
}

// parseDuration parses a human-readable duration string.
// Supports: "30m", "1h", "6h", "1d", "1w", "2h30m".
func parseDuration(s string) (time.Duration, error) {
	// Handle day/week shortcuts
	if len(s) > 0 {
		switch s[len(s)-1] {
		case 'd':
			// Parse as days
			var days int
			if _, err := fmt.Sscanf(s, "%dd", &days); err == nil {
				return time.Duration(days) * 24 * time.Hour, nil
			}
		case 'w':
			var weeks int
			if _, err := fmt.Sscanf(s, "%dw", &weeks); err == nil {
				return time.Duration(weeks) * 7 * 24 * time.Hour, nil
			}
		}
	}

	return time.ParseDuration(s)
}

// countFields counts whitespace-separated fields in a string.
func countFields(s string) int {
	count := 0
	inField := false
	for _, c := range s {
		if c == ' ' || c == '\t' {
			inField = false
		} else if !inField {
			count++
			inField = true
		}
	}
	return count
}

// ============================================================================
// Default action configs
// ============================================================================

// AnalyzeConfig is the config for the "analyze" action.
type AnalyzeConfig struct {
	ReportType string `json:"report_type"` // "daily", "event_triggered"
	Reason     string `json:"reason"`
}

// RunToolConfig is the config for the "run_tool" action.
type RunToolConfig struct {
	ToolName  string                 `json:"tool_name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// SendMessageConfig is the config for the "send_message" action.
type SendMessageConfig struct {
	Message string `json:"message"`
	Target  string `json:"target"` // "director_chat", "memory"
}

// ParseAnalyzeConfig parses action_config for analyze action.
func ParseAnalyzeConfig(configJSON string) *AnalyzeConfig {
	if configJSON == "" {
		return &AnalyzeConfig{ReportType: "daily", Reason: "scheduled cron job"}
	}
	var cfg AnalyzeConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return &AnalyzeConfig{ReportType: "daily", Reason: "scheduled cron job"}
	}
	if cfg.ReportType == "" {
		cfg.ReportType = "daily"
	}
	if cfg.Reason == "" {
		cfg.Reason = "scheduled cron job"
	}
	return &cfg
}

// ParseRunToolConfig parses action_config for run_tool action.
func ParseRunToolConfig(configJSON string) (*RunToolConfig, error) {
	if configJSON == "" {
		return nil, fmt.Errorf("action_config required for run_tool action")
	}
	var cfg RunToolConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return nil, fmt.Errorf("invalid run_tool config: %w", err)
	}
	if cfg.ToolName == "" {
		return nil, fmt.Errorf("tool_name required in run_tool config")
	}
	return &cfg, nil
}

// ParseSendMessageConfig parses action_config for send_message action.
func ParseSendMessageConfig(configJSON string) (*SendMessageConfig, error) {
	if configJSON == "" {
		return nil, fmt.Errorf("action_config required for send_message action")
	}
	var cfg SendMessageConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return nil, fmt.Errorf("invalid send_message config: %w", err)
	}
	if cfg.Message == "" {
		return nil, fmt.Errorf("message required in send_message config")
	}
	if cfg.Target == "" {
		cfg.Target = "memory"
	}
	return &cfg, nil
}
