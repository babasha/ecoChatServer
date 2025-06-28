package queries

import (
    "database/sql"
    "github.com/google/uuid"
    "github.com/egor/ecochatserver/database"
)

// nullStringToPointer использует общую функцию из database пакета
func nullStringToPointer(ns sql.NullString) *string {
    return database.NullStringToPointer(ns)
}

// nullUUIDToPointer использует общую функцию из database пакета
func nullUUIDToPointer(ns sql.NullString) (*uuid.UUID, error) {
    return database.NullUUIDToPointer(ns)
}