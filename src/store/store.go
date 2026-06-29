// Package store defines object storage backends.
package store

import (
	"errors"
	"fmt"
)

var ErrNotFound = errors.New("object not found")

// ConflictError reports the object which caused an ID or content conflict.
type ConflictError struct {
	ExistingID string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("object conflicts with %q", e.ExistingID)
}

// Store persists immutable objects in bucket-local namespaces.
type Store interface {
	Create(bucket, objectID string, data []byte) error
	Get(bucket, objectID string) ([]byte, error)
	Delete(bucket, objectID string) error
}
