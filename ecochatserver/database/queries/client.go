package queries

import (
    "context"
    "database/sql"
    "log"
    "time"

    "github.com/google/uuid"
)

func getClientUUIDByAPIKey(ctx context.Context, tx *sql.Tx, apiKey string) (uuid.UUID, error) {
    log.Printf("getClientUUIDByAPIKey: начало, apiKey=%s", apiKey)
    
    if u, err := uuid.Parse(apiKey); err == nil {
        log.Printf("getClientUUIDByAPIKey: apiKey является UUID: %s", u)
        return u, nil
    }
    
    var clientID uuid.UUID
    err := tx.QueryRowContext(ctx,
        "SELECT id FROM clients WHERE api_key=$1", apiKey,
    ).Scan(&clientID)
    
    if err == sql.ErrNoRows {
        clientID = uuid.New()
        log.Printf("getClientUUIDByAPIKey: клиент не найден, создаем новый ID=%s для apiKey=%s", 
            clientID, apiKey)
        
        if _, err := tx.ExecContext(ctx,
            "INSERT INTO clients(id,name,api_key,subscription,active,created_at) VALUES($1,$2,$3,'free',true,$4)",
            clientID, "Клиент "+apiKey, apiKey, time.Now(),
        ); err != nil {
            log.Printf("getClientUUIDByAPIKey: ошибка создания клиента: %v", err)
            return uuid.Nil, err
        }
        log.Printf("getClientUUIDByAPIKey: клиент создан ID=%s для apiKey=%s", clientID, apiKey)
    } else if err != nil {
        log.Printf("getClientUUIDByAPIKey: ошибка поиска клиента: %v", err)
        return uuid.Nil, err
    } else {
        log.Printf("getClientUUIDByAPIKey: найден существующий клиент ID=%s для apiKey=%s", 
            clientID, apiKey)
    }
    
    return clientID, nil
}

func EnsureClientWithAPIKey(db *sql.DB, apiKey, clientName string) (uuid.UUID, error) {
    log.Printf("EnsureClientWithAPIKey: начало, apiKey=%s, clientName=%s", apiKey, clientName)
    
    ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
    defer cancel()

    tx, err := db.BeginTx(ctx, nil)
    if err != nil {
        log.Printf("EnsureClientWithAPIKey: ошибка начала транзакции: %v", err)
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
        log.Printf("EnsureClientWithAPIKey: создаем нового клиента ID=%s, name=%s", clientID, clientName)
        
        if _, err := tx.ExecContext(ctx,
            "INSERT INTO clients(id,name,api_key,subscription,active,created_at) VALUES($1,$2,$3,'free',true,$4)",
            clientID, clientName, apiKey, time.Now(),
        ); err != nil {
            log.Printf("EnsureClientWithAPIKey: ошибка создания клиента: %v", err)
            return uuid.Nil, err
        }
    } else if err != nil {
        log.Printf("EnsureClientWithAPIKey: ошибка поиска клиента: %v", err)
        return uuid.Nil, err
    } else {
        log.Printf("EnsureClientWithAPIKey: найден существующий клиент ID=%s", clientID)
    }

    if err := tx.Commit(); err != nil {
        log.Printf("EnsureClientWithAPIKey: ошибка коммита: %v", err)
        return uuid.Nil, err
    }
    
    log.Printf("EnsureClientWithAPIKey: успешно, возвращаем clientID=%s", clientID)
    return clientID, nil
}