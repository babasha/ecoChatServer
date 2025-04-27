// internal/database/helpers.go
package database

import "database/sql"

// nullStringToPointer превращает sql.NullString → *string.
// Функция должна существовать один раз во всём пакете database.
func nullStringToPointer(ns sql.NullString) *string {
	if ns.Valid {
		s := ns.String
		return &s
	}
	return nil
}