package store

import (
	"bytes"
	"context"
	"sync"
)

type memoryObject struct {
	data []byte
	hash contentDigest
}

type memoryBucket struct {
	byID   map[string]memoryObject
	byHash map[contentDigest][]string
}

func newMemoryBucket() *memoryBucket {
	return &memoryBucket{
		byID:   make(map[string]memoryObject),
		byHash: make(map[contentDigest][]string),
	}
}

func (b *memoryBucket) duplicateID(hash contentDigest, data []byte) (string, bool) {
	for _, objectID := range b.byHash[hash] {
		if bytes.Equal(b.byID[objectID].data, data) {
			return objectID, true
		}
	}
	return "", false
}

func (b *memoryBucket) add(objectID string, object memoryObject) {
	b.byID[objectID] = object
	b.byHash[object.hash] = append(b.byHash[object.hash], objectID)
}

func (b *memoryBucket) remove(objectID string) {
	object := b.byID[objectID]
	delete(b.byID, objectID)

	ids := b.byHash[object.hash]
	for index, indexedID := range ids {
		if indexedID == objectID {
			ids = append(ids[:index], ids[index+1:]...)
			break
		}
	}
	if len(ids) == 0 {
		delete(b.byHash, object.hash)
		return
	}
	b.byHash[object.hash] = ids
}

// Memory stores objects in process memory.
type Memory struct {
	mu      sync.RWMutex
	buckets map[string]*memoryBucket
}

// NewMemory creates an empty in-memory object store.
func NewMemory() *Memory {
	return &Memory{buckets: make(map[string]*memoryBucket)}
}

// Create stores an immutable object unless its ID or content already exists.
func (s *Memory) Create(ctx context.Context, bucket, objectID string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	storedData := bytes.Clone(data)
	hash := digestContent(storedData)

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}

	bucketState := s.buckets[bucket]
	if bucketState == nil {
		bucketState = newMemoryBucket()
		s.buckets[bucket] = bucketState
	}
	if _, exists := bucketState.byID[objectID]; exists {
		return &ConflictError{ExistingID: objectID}
	}
	if existingID, exists := bucketState.duplicateID(hash, storedData); exists {
		return &ConflictError{ExistingID: existingID}
	}

	bucketState.add(objectID, memoryObject{data: storedData, hash: hash})
	return nil
}

// Get returns a copy of the requested object's data.
func (s *Memory) Get(ctx context.Context, bucket, objectID string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	bucketState := s.buckets[bucket]
	if bucketState == nil {
		return nil, ErrNotFound
	}
	object, exists := bucketState.byID[objectID]
	if !exists {
		return nil, ErrNotFound
	}
	return bytes.Clone(object.data), nil
}

// Delete removes an object.
func (s *Memory) Delete(ctx context.Context, bucket, objectID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}

	bucketState := s.buckets[bucket]
	if bucketState == nil {
		return ErrNotFound
	}
	if _, exists := bucketState.byID[objectID]; !exists {
		return ErrNotFound
	}
	bucketState.remove(objectID)
	if len(bucketState.byID) == 0 {
		delete(s.buckets, bucket)
	}
	return nil
}
