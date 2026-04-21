// Package filestore provides JSONL-backed implementations of the durable
// repository contracts. v1 choice — swap to Mongo/Postgres later via the
// same interfaces.
package filestore

import "errors"

// ErrNotFound is returned by Get-style lookups when no record matches the key.
var ErrNotFound = errors.New("record not found")
