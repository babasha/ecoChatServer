package queries

import (
    "database/sql"
    "github.com/google/uuid"
)

// nullStringToPointer преобразует sql.NullString в *string
func nullStringToPointer(ns sql.NullString) *string {
    if ns.Valid {
        s := ns.String
        return &s
    }
    return nil
}

// nullUUIDToPointer преобразует sql.NullString в *uuid.UUID
func nullUUIDToPointer(ns sql.NullString) (*uuid.UUID, error) {
    if !ns.Valid {
        return nil, nil
    }
    u, err := uuid.Parse(ns.String)
    if err != nil {
        return nil, err
    }
    return &u, nil
}