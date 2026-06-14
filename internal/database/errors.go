package database

import "errors"

// ErrNotFound is returned by repository lookups when no row matches. Callers
// distinguish it with errors.Is to map to a 404 (or "create" path) rather than a
// 500.
var ErrNotFound = errors.New("database: not found")
