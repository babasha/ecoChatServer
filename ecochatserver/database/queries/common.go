package queries

import (
    "time"
)

const (
    DefaultPageSize = 20
    MaxPageSize     = 100
    dbQueryTimeout  = 5 * time.Second
)