// Package store provides memory and disk object-storage backends.
package store

import (
	"errors"
	"fmt"
)

// ErrNotFound indicates that an object or bucket does not exist.
var ErrNotFound = errors.New("object not found")

// ConflictError reports the object which caused an ID or content conflict.
type ConflictError struct {
	ExistingID string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("object conflicts with %q", e.ExistingID)
}
