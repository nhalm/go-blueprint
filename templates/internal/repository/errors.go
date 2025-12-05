package repository

import "errors"

// ErrNotFound is returned when a requested resource doesn't exist.
// This abstracts the generated code's error so service layer doesn't
// depend on generated internals.
var ErrNotFound = errors.New("not found")
