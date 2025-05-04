package queries

import (
    "context"
    "database/sql"
    "log"
    "time"

    "github.com/google/uuid"
)

func getClientUUIDByAPIKey(ctx context.Context, tx *sql.Tx, apiKey string) (uuid.UUID, error) {
    if u, err := uuid.Parse(apiKey); err == nil {
        return u, nil
    }
    var clientID uuid.UUID
    err := tx.QueryRowContext(ctx,
        "SELECT id FROM clients WHERE api_key=$1", apiKey,
    ).Scan(&clientID)
    if err == sql.ErrNoRows {
        clientID = uuid.New()
        if _, err := tx.ExecContext(ctx,
            "INSERT INTO clients(id,name,api_key,subscription,active,created_at) VALUES($1,$2,$3,'free',true,$4)",
            clientID, "Клиент "+apiKey, apiKey, time.Now(),
        ); err != nil {
            return uuid.Nil, err
        }
        log.Printf("Created client %s for key %s", clientID, apiKey)
    } else if err != nil {
        return uuid.Nil, err
    }
    return clientID, nil
}

func EnsureClientWithAPIKey(db *sql.DB, apiKey, clientName string) (uuid.UUID, error) {
    ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
    defer cancel()

    tx, err := db.BeginTx(ctx, nil)
    if err != nil {
        return uuid.Nil, err
    }
    defer tx.Rollback()

    var clientID uuid.UUID
    err = tx.QueryRowContext(ctx,
        "SELECT id FROM clients WHERE api_key=$1", apiKey,
    ).Scan(&clientID)
    if err == sql.ErrNoRows {
        clientID = uuid.New()
        if clientName == "" {
            clientName = "Клиент " + apiKey
        }
        if _, err := tx.ExecContext(ctx,
            "INSERT INTO clients(id,name,api_key,subscription,active,created_at) VALUES($1,$2,$3,'free',true,$4)",
            clientID, clientName, apiKey, time.Now(),
        ); err != nil {
            return uuid.Nil, err
        }
    } else if err != nil {
        return uuid.Nil, err
    }

    if err := tx.Commit(); err != nil {
        return uuid.Nil, err
    }
    return clientID, nil
}