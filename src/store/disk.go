package store

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Disk stores each object as a file beneath root.
type Disk struct {
	root string
	mu   sync.RWMutex
}

func NewDisk(root string) (*Disk, error) {
	if root == "" {
		return nil, errors.New("disk data directory cannot be empty")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create disk data directory: %w", err)
	}
	return &Disk{root: root}, nil
}

func (s *Disk) Create(bucket, objectID string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	bucketPath := s.bucketPath(bucket)
	if err := os.MkdirAll(bucketPath, 0o700); err != nil {
		return fmt.Errorf("create bucket directory: %w", err)
	}

	objectPath := filepath.Join(bucketPath, encodeName(objectID))
	if _, err := os.Stat(objectPath); err == nil {
		return &ConflictError{ExistingID: objectID}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect object: %w", err)
	}

	entries, err := os.ReadDir(bucketPath)
	if err != nil {
		return fmt.Errorf("read bucket directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || len(entry.Name()) > 0 && entry.Name()[0] == '.' {
			continue
		}
		existingData, err := os.ReadFile(filepath.Join(bucketPath, entry.Name()))
		if err != nil {
			return fmt.Errorf("read existing object: %w", err)
		}
		if bytes.Equal(existingData, data) {
			existingID, err := decodeName(entry.Name())
			if err != nil {
				return fmt.Errorf("decode existing object ID: %w", err)
			}
			return &ConflictError{ExistingID: existingID}
		}
	}

	temporary, err := os.CreateTemp(bucketPath, ".object-*")
	if err != nil {
		return fmt.Errorf("create temporary object: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)

	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("set object permissions: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write object: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync object: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close object: %w", err)
	}
	if err := os.Link(temporaryName, objectPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return &ConflictError{ExistingID: objectID}
		}
		return fmt.Errorf("commit object: %w", err)
	}
	return nil
}

func (s *Disk) Get(bucket, objectID string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.objectPath(bucket, objectID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read object: %w", err)
	}
	return data, nil
}

func (s *Disk) Delete(bucket, objectID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.Remove(s.objectPath(bucket, objectID)); errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

func (s *Disk) bucketPath(bucket string) string {
	return filepath.Join(s.root, encodeName(bucket))
}

func (s *Disk) objectPath(bucket, objectID string) string {
	return filepath.Join(s.bucketPath(bucket), encodeName(objectID))
}

func encodeName(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeName(value string) (string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return string(decoded), err
}
