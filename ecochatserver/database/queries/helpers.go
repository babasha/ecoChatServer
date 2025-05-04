package queries

import (
    "database/sql"
    "github.com/google/uuid"
)

func nullStringToPointer(ns sql.NullString) *string {
    if ns.Valid {
        s := ns.String
        return &s
    }
    return nil
}

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