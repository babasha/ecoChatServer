package queries

import (
    "database/sql"
    "github.com/google/uuid"
    "github.com/egor/ecochatserver/database"
)

// nullStringToPointer - проксируем через пакет database
func nullStringToPointer(ns sql.NullString) *string {
    return database.nullStringToPointer(ns)
}

// nullUUIDToPointer конвертирует sql.NullString в *uuid.UUID
func nullUUIDToPointer(ns sql.NullString) (*uuid.UUID, error) {
    if !ns.Valid || ns.String == "" {
        return nil, nil
    }
    u, err := uuid.Parse(ns.String)
    if err != nil {
        return nil, err
    }
    return &u, nil
}