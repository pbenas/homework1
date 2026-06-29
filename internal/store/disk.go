package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// Disk stores each object as a file beneath root. A persistent, per-bucket
// SHA-256 index makes duplicate lookups independent of the number of objects.
// File locks serialize mutations across Disk values and server processes that
// share the same root.
type Disk struct {
	root     string
	fs       *os.Root
	mu       sync.RWMutex
	closed   bool
	stop     runtime.Cleanup
	closeErr error
}

// NewDisk opens or creates a disk-backed object store rooted at root.
func NewDisk(root string) (*Disk, error) {
	if !diskLockSupported() {
		return nil, errors.New("disk backend is unsupported on this operating system")
	}
	if root == "" {
		return nil, errors.New("disk data directory cannot be empty")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create disk data directory: %w", err)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve disk data directory: %w", err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve absolute disk data directory: %w", err)
	}
	rootFS, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open disk data directory: %w", err)
	}
	storage := &Disk{
		root: root,
		fs:   rootFS,
	}
	storage.stop = runtime.AddCleanup(storage, func(root *os.Root) {
		_ = root.Close()
	}, rootFS)
	return storage, nil
}

// Close releases the file descriptor used to anchor root-relative operations.
// The cleanup registered by NewDisk is a fallback; callers should close Disk
// explicitly when the server shuts down.
func (s *Disk) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return s.closeErr
	}
	s.closed = true
	s.stop.Stop()
	s.closeErr = s.fs.Close()
	return s.closeErr
}

// Create stores an immutable object unless its ID or content already exists.
func (s *Disk) Create(ctx context.Context, bucket, objectID string, data []byte) error {
	// Hashing can dominate large creates, so keep it outside the lifecycle and
	// bucket locks. Only the index/object commit needs serialization.
	hash := digestContent(data).String()
	if err := s.beginOperation(ctx); err != nil {
		return err
	}
	defer s.endOperation()

	return s.withBucketLock(ctx, bucket, exclusiveLock, func() error {
		bucketPath, err := s.ensureBucket(bucket)
		if err != nil {
			return err
		}
		objectPath := filepath.Join(bucketPath, encodeName(objectID))
		if exists, err := s.regularFileExists(objectPath); err != nil {
			return fmt.Errorf("inspect object: %w", err)
		} else if exists {
			return &ConflictError{ExistingID: objectID}
		}

		indexPath := filepath.Join(bucketPath, indexDirectory, hash)
		existingID, err := s.readRegularFile(indexPath)
		if err == nil {
			decoded, decodeErr := decodeName(string(existingID))
			if decodeErr != nil || encodeName(decoded) != string(existingID) {
				return errors.New("content index contains an invalid object ID")
			}
			existingData, readErr := s.readRegularFile(filepath.Join(bucketPath, string(existingID)))
			if errors.Is(readErr, os.ErrNotExist) {
				// Recover an index reservation left behind by an interrupted create.
				if removeErr := s.fs.Remove(indexPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
					return fmt.Errorf("remove stale content index: %w", removeErr)
				}
				err = os.ErrNotExist
			} else if readErr != nil {
				return fmt.Errorf("verify content index: %w", readErr)
			} else if !bytes.Equal(existingData, data) {
				return errors.New("content hash collision in disk index")
			} else {
				return &ConflictError{ExistingID: decoded}
			}
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read content index: %w", err)
		}

		if err := s.writeFileExclusive(indexPath, []byte(encodeName(objectID))); err != nil {
			if errors.Is(err, os.ErrExist) {
				return errors.New("content index changed while bucket was locked")
			}
			return fmt.Errorf("commit content index: %w", err)
		}
		if err := s.writeFileExclusive(objectPath, data); err != nil {
			_ = s.fs.Remove(indexPath)
			if errors.Is(err, os.ErrExist) {
				return &ConflictError{ExistingID: objectID}
			}
			return fmt.Errorf("commit object: %w", err)
		}
		return nil
	})
}

// Get returns the requested object's data.
func (s *Disk) Get(ctx context.Context, bucket, objectID string) ([]byte, error) {
	if err := s.beginOperation(ctx); err != nil {
		return nil, err
	}
	defer s.endOperation()

	var data []byte
	err := s.withBucketLock(ctx, bucket, sharedLock, func() error {
		bucketPath, err := s.existingBucket(bucket)
		if err != nil {
			return err
		}
		data, err = s.readRegularFile(filepath.Join(bucketPath, encodeName(objectID)))
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("read object: %w", err)
		}
		return nil
	})
	return data, err
}

// Delete removes an object and its content-index entry.
func (s *Disk) Delete(ctx context.Context, bucket, objectID string) error {
	if err := s.beginOperation(ctx); err != nil {
		return err
	}
	defer s.endOperation()

	return s.withBucketLock(ctx, bucket, exclusiveLock, func() error {
		bucketPath, err := s.existingBucket(bucket)
		if err != nil {
			return err
		}
		objectPath := filepath.Join(bucketPath, encodeName(objectID))
		data, err := s.readRegularFile(objectPath)
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("read object before deletion: %w", err)
		}
		if err := s.fs.Remove(objectPath); err != nil {
			return fmt.Errorf("delete object: %w", err)
		}

		indexPath := filepath.Join(bucketPath, indexDirectory, digestContent(data).String())
		indexedID, err := s.readRegularFile(indexPath)
		if err == nil && string(indexedID) == encodeName(objectID) {
			if err := s.fs.Remove(indexPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("delete content index: %w", err)
			}
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read content index: %w", err)
		}
		return nil
	})
}

func (s *Disk) beginOperation(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return errors.New("disk store is closed")
	}
	if err := ctx.Err(); err != nil {
		s.mu.RUnlock()
		return err
	}
	return nil
}

func (s *Disk) endOperation() {
	s.mu.RUnlock()
}
