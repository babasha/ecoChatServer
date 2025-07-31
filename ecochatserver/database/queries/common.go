package queries

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const (
	DefaultPageSize = 20
	MaxPageSize     = 100
	dbQueryTimeout  = 5 * time.Second
)

// WithDBContext создает контекст с таймаутом для DB операций
func WithDBContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), dbQueryTimeout)
}

// ExecInTx выполняет функцию в транзакции с автоматическим rollback при ошибке
func ExecInTx(db *sql.DB, fn func(*sql.Tx) error) error {
	ctx, cancel := WithDBContext()
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	return nil
}
