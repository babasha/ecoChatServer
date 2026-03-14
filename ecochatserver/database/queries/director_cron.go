package queries

import (
	"database/sql"
	"time"

	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// InsertCronJob creates a new cron job.
func InsertCronJob(db *sql.DB, job *models.CronJob) error {
	_, err := db.Exec(`
		INSERT INTO director_cron_jobs
			(id, name, description, schedule_type, schedule, timezone,
			 action, action_config, enabled, created_by, next_run_at, max_runs,
			 created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		job.ID, job.Name, job.Description, job.ScheduleType, job.Schedule,
		job.Timezone, job.Action, job.ActionConfig, job.Enabled, job.CreatedBy,
		job.NextRunAt, job.MaxRuns, job.CreatedAt, job.UpdatedAt,
	)
	return err
}

// GetCronJob returns a single cron job by ID.
func GetCronJob(db *sql.DB, id uuid.UUID) (*models.CronJob, error) {
	var job models.CronJob
	err := db.QueryRow(`
		SELECT id, name, description, schedule_type, schedule, timezone,
		       action, action_config, enabled, created_by,
		       last_run_at, next_run_at, run_count, COALESCE(last_error, ''),
		       max_runs, created_at, updated_at
		FROM director_cron_jobs WHERE id = $1`, id).Scan(
		&job.ID, &job.Name, &job.Description, &job.ScheduleType, &job.Schedule,
		&job.Timezone, &job.Action, &job.ActionConfig, &job.Enabled, &job.CreatedBy,
		&job.LastRunAt, &job.NextRunAt, &job.RunCount, &job.LastError,
		&job.MaxRuns, &job.CreatedAt, &job.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &job, err
}

// GetCronJobByName returns a cron job by name (unique).
func GetCronJobByName(db *sql.DB, name string) (*models.CronJob, error) {
	var job models.CronJob
	err := db.QueryRow(`
		SELECT id, name, description, schedule_type, schedule, timezone,
		       action, action_config, enabled, created_by,
		       last_run_at, next_run_at, run_count, COALESCE(last_error, ''),
		       max_runs, created_at, updated_at
		FROM director_cron_jobs WHERE name = $1`, name).Scan(
		&job.ID, &job.Name, &job.Description, &job.ScheduleType, &job.Schedule,
		&job.Timezone, &job.Action, &job.ActionConfig, &job.Enabled, &job.CreatedBy,
		&job.LastRunAt, &job.NextRunAt, &job.RunCount, &job.LastError,
		&job.MaxRuns, &job.CreatedAt, &job.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &job, err
}

// ListCronJobs returns all cron jobs, ordered by creation time.
func ListCronJobs(db *sql.DB) ([]models.CronJob, error) {
	rows, err := db.Query(`
		SELECT id, name, description, schedule_type, schedule, timezone,
		       action, action_config, enabled, created_by,
		       last_run_at, next_run_at, run_count, COALESCE(last_error, ''),
		       max_runs, created_at, updated_at
		FROM director_cron_jobs
		ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []models.CronJob
	for rows.Next() {
		var job models.CronJob
		if err := rows.Scan(
			&job.ID, &job.Name, &job.Description, &job.ScheduleType, &job.Schedule,
			&job.Timezone, &job.Action, &job.ActionConfig, &job.Enabled, &job.CreatedBy,
			&job.LastRunAt, &job.NextRunAt, &job.RunCount, &job.LastError,
			&job.MaxRuns, &job.CreatedAt, &job.UpdatedAt,
		); err != nil {
			continue
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

// GetEnabledCronJobs returns only enabled cron jobs.
func GetEnabledCronJobs(db *sql.DB) ([]models.CronJob, error) {
	rows, err := db.Query(`
		SELECT id, name, description, schedule_type, schedule, timezone,
		       action, action_config, enabled, created_by,
		       last_run_at, next_run_at, run_count, COALESCE(last_error, ''),
		       max_runs, created_at, updated_at
		FROM director_cron_jobs
		WHERE enabled = true
		ORDER BY next_run_at ASC NULLS LAST`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []models.CronJob
	for rows.Next() {
		var job models.CronJob
		if err := rows.Scan(
			&job.ID, &job.Name, &job.Description, &job.ScheduleType, &job.Schedule,
			&job.Timezone, &job.Action, &job.ActionConfig, &job.Enabled, &job.CreatedBy,
			&job.LastRunAt, &job.NextRunAt, &job.RunCount, &job.LastError,
			&job.MaxRuns, &job.CreatedAt, &job.UpdatedAt,
		); err != nil {
			continue
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

// UpdateCronJob updates mutable fields of a cron job.
func UpdateCronJob(db *sql.DB, id uuid.UUID, description, schedule, timezone, actionConfig *string, enabled *bool) error {
	_, err := db.Exec(`
		UPDATE director_cron_jobs SET
			description   = COALESCE($2, description),
			schedule      = COALESCE($3, schedule),
			timezone      = COALESCE($4, timezone),
			action_config = COALESCE($5, action_config),
			enabled       = COALESCE($6, enabled),
			updated_at    = NOW()
		WHERE id = $1`, id, description, schedule, timezone, actionConfig, enabled)
	return err
}

// UpdateCronJobRun records that a job has been executed.
func UpdateCronJobRun(db *sql.DB, id uuid.UUID, lastError string, nextRunAt *time.Time) error {
	_, err := db.Exec(`
		UPDATE director_cron_jobs SET
			last_run_at = NOW(),
			run_count   = run_count + 1,
			last_error  = $2,
			next_run_at = $3,
			updated_at  = NOW()
		WHERE id = $1`, id, lastError, nextRunAt)
	return err
}

// DisableCronJob disables a cron job (e.g., after max_runs reached).
func DisableCronJob(db *sql.DB, id uuid.UUID) error {
	_, err := db.Exec(`
		UPDATE director_cron_jobs SET enabled = false, updated_at = NOW()
		WHERE id = $1`, id)
	return err
}

// DeleteCronJob removes a cron job.
func DeleteCronJob(db *sql.DB, id uuid.UUID) error {
	_, err := db.Exec(`DELETE FROM director_cron_jobs WHERE id = $1`, id)
	return err
}

// InsertCronRunLog records a cron execution.
func InsertCronRunLog(db *sql.DB, log *models.CronRunLog) error {
	_, err := db.Exec(`
		INSERT INTO director_cron_run_log (id, job_id, status, duration_ms, error, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		log.ID, log.JobID, log.Status, log.Duration, log.Error, log.CreatedAt)
	return err
}

// GetCronRunLogs returns recent run logs for a job.
func GetCronRunLogs(db *sql.DB, jobID uuid.UUID, limit int) ([]models.CronRunLog, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := db.Query(`
		SELECT id, job_id, status, duration_ms, COALESCE(error, ''), created_at
		FROM director_cron_run_log
		WHERE job_id = $1
		ORDER BY created_at DESC LIMIT $2`, jobID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []models.CronRunLog
	for rows.Next() {
		var l models.CronRunLog
		if err := rows.Scan(&l.ID, &l.JobID, &l.Status, &l.Duration, &l.Error, &l.CreatedAt); err != nil {
			continue
		}
		logs = append(logs, l)
	}
	return logs, nil
}

// CleanupOldCronRunLogs removes old run logs.
func CleanupOldCronRunLogs(db *sql.DB, retentionDays int) (int64, error) {
	result, err := db.Exec(`
		DELETE FROM director_cron_run_log
		WHERE created_at < NOW() - ($1 || ' days')::INTERVAL`, retentionDays)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
