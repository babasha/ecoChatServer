package database

import (
    "database/sql"
    "errors"
    "github.com/google/uuid"
)

// NullStringToPointer превращает sql.NullString → *string.
// Функция экспортируется для использования в других пакетах
func NullStringToPointer(ns sql.NullString) *string {
    if ns.Valid {
        s := ns.String
        return &s
    }
    return nil
}

// StringToUUID конвертирует строку в UUID
func StringToUUID(s string) (uuid.UUID, error) {
    if s == "" {
        return uuid.Nil, errors.New("empty UUID string")
    }
    return uuid.Parse(s)
}

// UUIDToString конвертирует UUID в строку
func UUIDToString(u uuid.UUID) string {
    if u == uuid.Nil {
        return ""
    }
    return u.String()
}

// NullUUIDToPointer конвертирует sql.NullString в *uuid.UUID
func NullUUIDToPointer(ns sql.NullString) (*uuid.UUID, error) {
    if !ns.Valid {
        return nil, nil
    }
    u, err := uuid.Parse(ns.String)
    if err != nil {
        return nil, err
    }
    return &u, nil
}

// UUIDPointerToNullString конвертирует *uuid.UUID в sql.NullString
func UUIDPointerToNullString(u *uuid.UUID) sql.NullString {
    if u == nil {
        return sql.NullString{Valid: false}
    }
    return sql.NullString{
        String: u.String(),
        Valid:  true,
    }
}