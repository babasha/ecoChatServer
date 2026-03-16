package queries

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

type DirectorTask struct {
	ID            uuid.UUID  `json:"id"`
	Title         string     `json:"title"`
	Description   string     `json:"description"`
	Status        string     `json:"status"`
	Priority      string     `json:"priority"`
	Category      string     `json:"category"`
	CreatedBy     string     `json:"created_by"`
	AssignedTo    string     `json:"assigned_to"`
	DueDate       *time.Time `json:"due_date"`
	CompletedAt   *time.Time `json:"completed_at"`
	FailureReason string     `json:"failure_reason"`
	Tags          []string   `json:"tags"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	Comments      []TaskComment `json:"comments,omitempty"`
}

type TaskComment struct {
	ID        uuid.UUID `json:"id"`
	TaskID    uuid.UUID `json:"task_id"`
	Author    string    `json:"author"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateTask inserts a new director task.
func CreateTask(db *sql.DB, title, description, priority, category, createdBy, assignedTo string, dueDate *time.Time, tags []string) (*DirectorTask, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	if priority == "" {
		priority = "medium"
	}
	if category == "" {
		category = "general"
	}
	if createdBy == "" {
		createdBy = "director"
	}
	if assignedTo == "" {
		assignedTo = "director"
	}
	if tags == nil {
		tags = []string{}
	}

	var t DirectorTask
	err := db.QueryRowContext(ctx, `
		INSERT INTO director_tasks (title, description, priority, category, created_by, assigned_to, due_date, tags)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, title, description, status, priority, category, created_by, assigned_to,
		          due_date, completed_at, failure_reason, tags, created_at, updated_at`,
		title, description, priority, category, createdBy, assignedTo, dueDate, pq.Array(tags),
	).Scan(&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.Category,
		&t.CreatedBy, &t.AssignedTo, &t.DueDate, &t.CompletedAt, &t.FailureReason,
		pq.Array(&t.Tags), &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create task: %w", err)
	}
	return &t, nil
}

// GetTasks returns tasks filtered by status.
func GetTasks(db *sql.DB, status string, limit int) ([]DirectorTask, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	if limit <= 0 || limit > 100 {
		limit = 50
	}

	var rows *sql.Rows
	var err error
	if status != "" {
		rows, err = db.QueryContext(ctx, `
			SELECT id, title, description, status, priority, category, created_by, assigned_to,
			       due_date, completed_at, failure_reason, tags, created_at, updated_at
			FROM director_tasks WHERE status = $1
			ORDER BY CASE priority WHEN 'high' THEN 0 WHEN 'medium' THEN 1 ELSE 2 END, created_at DESC
			LIMIT $2`, status, limit)
	} else {
		rows, err = db.QueryContext(ctx, `
			SELECT id, title, description, status, priority, category, created_by, assigned_to,
			       due_date, completed_at, failure_reason, tags, created_at, updated_at
			FROM director_tasks
			ORDER BY CASE status WHEN 'in_progress' THEN 0 WHEN 'pending' THEN 1 WHEN 'completed' THEN 2 WHEN 'failed' THEN 3 ELSE 4 END,
			         CASE priority WHEN 'high' THEN 0 WHEN 'medium' THEN 1 ELSE 2 END,
			         created_at DESC
			LIMIT $1`, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("get tasks: %w", err)
	}
	defer rows.Close()

	return scanTasks(rows)
}

// GetTaskWithComments returns a single task with all its comments.
func GetTaskWithComments(db *sql.DB, taskID uuid.UUID) (*DirectorTask, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	var t DirectorTask
	err := db.QueryRowContext(ctx, `
		SELECT id, title, description, status, priority, category, created_by, assigned_to,
		       due_date, completed_at, failure_reason, tags, created_at, updated_at
		FROM director_tasks WHERE id = $1`, taskID,
	).Scan(&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.Category,
		&t.CreatedBy, &t.AssignedTo, &t.DueDate, &t.CompletedAt, &t.FailureReason,
		pq.Array(&t.Tags), &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}

	// Load comments
	cRows, err := db.QueryContext(ctx, `
		SELECT id, task_id, author, content, created_at
		FROM director_task_comments WHERE task_id = $1
		ORDER BY created_at ASC`, taskID)
	if err == nil {
		defer cRows.Close()
		for cRows.Next() {
			var c TaskComment
			if scanErr := cRows.Scan(&c.ID, &c.TaskID, &c.Author, &c.Content, &c.CreatedAt); scanErr != nil {
				continue
			}
			t.Comments = append(t.Comments, c)
		}
		if cErr := cRows.Err(); cErr != nil {
			return nil, fmt.Errorf("scan task comments: %w", cErr)
		}
	}

	return &t, nil
}

// UpdateTaskStatus changes task status with optional failure reason.
func UpdateTaskStatus(db *sql.DB, taskID uuid.UUID, status, failureReason string) error {
	ctx, cancel := WithDBContext()
	defer cancel()

	var completedAt *time.Time
	if status == "completed" || status == "failed" {
		now := time.Now()
		completedAt = &now
	}

	result, err := db.ExecContext(ctx, `
		UPDATE director_tasks
		SET status = $2, failure_reason = $3, completed_at = $4, updated_at = NOW()
		WHERE id = $1`, taskID, status, failureReason, completedAt)
	if err != nil {
		return fmt.Errorf("update task status: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return fmt.Errorf("task not found: %s", taskID)
	}
	return nil
}

// AddTaskComment adds a comment to a task.
func AddTaskComment(db *sql.DB, taskID uuid.UUID, author, content string) (*TaskComment, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	var c TaskComment
	err := db.QueryRowContext(ctx, `
		INSERT INTO director_task_comments (task_id, author, content)
		VALUES ($1, $2, $3)
		RETURNING id, task_id, author, content, created_at`,
		taskID, author, content,
	).Scan(&c.ID, &c.TaskID, &c.Author, &c.Content, &c.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("add task comment: %w", err)
	}
	return &c, nil
}

// DeleteTask removes a task.
func DeleteTask(db *sql.DB, taskID uuid.UUID) error {
	ctx, cancel := WithDBContext()
	defer cancel()

	result, err := db.ExecContext(ctx, `DELETE FROM director_tasks WHERE id = $1`, taskID)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return fmt.Errorf("task not found: %s", taskID)
	}
	return nil
}

func scanTasks(rows *sql.Rows) ([]DirectorTask, error) {
	var tasks []DirectorTask
	for rows.Next() {
		var t DirectorTask
		if err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.Category,
			&t.CreatedBy, &t.AssignedTo, &t.DueDate, &t.CompletedAt, &t.FailureReason,
			pq.Array(&t.Tags), &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}
