package store

import (
	"bytes"
	"sync"
)

// Memory stores objects in process memory.
type Memory struct {
	mu      sync.RWMutex
	buckets map[string]map[string][]byte
}

func NewMemory() *Memory {
	return &Memory{buckets: make(map[string]map[string][]byte)}
}

func (s *Memory) Create(bucket, objectID string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	objects := s.buckets[bucket]
	if objects == nil {
		objects = make(map[string][]byte)
		s.buckets[bucket] = objects
	}
	if _, exists := objects[objectID]; exists {
		return &ConflictError{ExistingID: objectID}
	}
	for existingID, existingData := range objects {
		if bytes.Equal(existingData, data) {
			return &ConflictError{ExistingID: existingID}
		}
	}

	objects[objectID] = bytes.Clone(data)
	return nil
}

func (s *Memory) Get(bucket, objectID string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, exists := s.buckets[bucket][objectID]
	if !exists {
		return nil, ErrNotFound
	}
	return bytes.Clone(data), nil
}

func (s *Memory) Delete(bucket, objectID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	objects := s.buckets[bucket]
	if _, exists := objects[objectID]; !exists {
		return ErrNotFound
	}
	delete(objects, objectID)
	if len(objects) == 0 {
		delete(s.buckets, bucket)
	}
	return nil
}
