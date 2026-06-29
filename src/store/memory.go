package store

import (
	"bytes"
	"crypto/sha256"
	"sync"
)

type memoryObject struct {
	data []byte
	hash [sha256.Size]byte
}

type memoryBucket struct {
	byID   map[string]memoryObject
	byHash map[[sha256.Size]byte][]string
}

// Memory stores objects in process memory.
type Memory struct {
	mu      sync.RWMutex
	buckets map[string]*memoryBucket
}

func NewMemory() *Memory {
	return &Memory{buckets: make(map[string]*memoryBucket)}
}

func (s *Memory) Create(bucket, objectID string, data []byte) error {
	storedData := bytes.Clone(data)
	hash := sha256.Sum256(storedData)

	s.mu.Lock()
	defer s.mu.Unlock()

	objects := s.buckets[bucket]
	if objects == nil {
		objects = &memoryBucket{
			byID:   make(map[string]memoryObject),
			byHash: make(map[[sha256.Size]byte][]string),
		}
		s.buckets[bucket] = objects
	}
	if _, exists := objects.byID[objectID]; exists {
		return &ConflictError{ExistingID: objectID}
	}
	for _, existingID := range objects.byHash[hash] {
		if bytes.Equal(objects.byID[existingID].data, storedData) {
			return &ConflictError{ExistingID: existingID}
		}
	}

	objects.byID[objectID] = memoryObject{data: storedData, hash: hash}
	objects.byHash[hash] = append(objects.byHash[hash], objectID)
	return nil
}

func (s *Memory) Get(bucket, objectID string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	objects := s.buckets[bucket]
	if objects == nil {
		return nil, ErrNotFound
	}
	object, exists := objects.byID[objectID]
	if !exists {
		return nil, ErrNotFound
	}
	return bytes.Clone(object.data), nil
}

func (s *Memory) Delete(bucket, objectID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	objects := s.buckets[bucket]
	if objects == nil {
		return ErrNotFound
	}
	object, exists := objects.byID[objectID]
	if !exists {
		return ErrNotFound
	}
	delete(objects.byID, objectID)
	IDs := objects.byHash[object.hash]
	for index, indexedID := range IDs {
		if indexedID == objectID {
			IDs = append(IDs[:index], IDs[index+1:]...)
			break
		}
	}
	if len(IDs) == 0 {
		delete(objects.byHash, object.hash)
	} else {
		objects.byHash[object.hash] = IDs
	}
	if len(objects.byID) == 0 {
		delete(s.buckets, bucket)
	}
	return nil
}
