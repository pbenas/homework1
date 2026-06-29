package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
)

const (
	indexDirectory = ".index"
	indexReadyFile = ".ready"
)

// Disk stores each object as a file beneath root. A persistent, per-bucket
// SHA-256 index makes duplicate lookups independent of the number of objects.
// File locks serialize mutations across Disk values and server processes that
// share the same root.
type Disk struct {
	root        string
	fs          *os.Root
	mu          sync.RWMutex
	closed      bool
	bucketMu    sync.Mutex
	bucketLocks map[string]*sync.RWMutex
	stop        runtime.Cleanup
	once        sync.Once
}

func NewDisk(root string) (*Disk, error) {
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
		root:        root,
		fs:          rootFS,
		bucketLocks: make(map[string]*sync.RWMutex),
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
	var closeErr error
	s.once.Do(func() {
		s.closed = true
		s.stop.Stop()
		closeErr = s.fs.Close()
	})
	return closeErr
}

func (s *Disk) Create(bucket, objectID string, data []byte) error {
	// Hashing can dominate large creates, so keep it outside the lifecycle and
	// bucket locks. Only the index/object commit needs serialization.
	hash := contentHash(data)
	if err := s.beginOperation(); err != nil {
		return err
	}
	defer s.endOperation()

	return s.withBucketLock(bucket, true, func() error {
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

func (s *Disk) Get(bucket, objectID string) ([]byte, error) {
	if err := s.beginOperation(); err != nil {
		return nil, err
	}
	defer s.endOperation()

	var data []byte
	err := s.withBucketLock(bucket, false, func() error {
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

func (s *Disk) Delete(bucket, objectID string) error {
	if err := s.beginOperation(); err != nil {
		return err
	}
	defer s.endOperation()

	return s.withBucketLock(bucket, true, func() error {
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

		indexPath := filepath.Join(bucketPath, indexDirectory, contentHash(data))
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

func (s *Disk) beginOperation() error {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return errors.New("disk store is closed")
	}
	return nil
}

func (s *Disk) endOperation() {
	s.mu.RUnlock()
}

func (s *Disk) withBucketLock(bucket string, exclusive bool, operation func() error) error {
	bucketLock := s.bucketLock(bucket)
	if exclusive {
		bucketLock.Lock()
		defer bucketLock.Unlock()
	} else {
		bucketLock.RLock()
		defer bucketLock.RUnlock()
	}

	lockPath := "." + encodeName(bucket) + ".lock"
	lockDescriptor, err := syscall.Open(filepath.Join(s.root, lockPath), syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("open bucket lock: %w", err)
	}
	lock := os.NewFile(uintptr(lockDescriptor), lockPath)
	defer lock.Close()
	lockMode := syscall.LOCK_SH
	if exclusive {
		lockMode = syscall.LOCK_EX
	}
	for {
		err = syscall.Flock(int(lock.Fd()), lockMode)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("lock bucket: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) //nolint:errcheck
	return operation()
}

func (s *Disk) bucketLock(bucket string) *sync.RWMutex {
	key := encodeName(bucket)
	s.bucketMu.Lock()
	defer s.bucketMu.Unlock()
	lock := s.bucketLocks[key]
	if lock == nil {
		lock = &sync.RWMutex{}
		s.bucketLocks[key] = lock
	}
	return lock
}

func (s *Disk) ensureBucket(bucket string) (string, error) {
	bucketPath := encodeName(bucket)
	if info, err := s.fs.Lstat(bucketPath); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("bucket path is not a regular directory")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := s.fs.Mkdir(bucketPath, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("create bucket directory: %w", err)
		}
	} else {
		return "", fmt.Errorf("inspect bucket directory: %w", err)
	}

	indexPath := filepath.Join(bucketPath, indexDirectory)
	if info, err := s.fs.Lstat(indexPath); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("content index path is not a regular directory")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := s.fs.Mkdir(indexPath, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("create content index: %w", err)
		}
	} else {
		return "", fmt.Errorf("inspect content index: %w", err)
	}
	readyPath := filepath.Join(indexPath, indexReadyFile)
	if info, err := s.fs.Lstat(readyPath); err == nil {
		if !info.Mode().IsRegular() {
			return "", errors.New("content index marker is not a regular file")
		}
		return bucketPath, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect content index marker: %w", err)
	}
	if err := s.rebuildIndex(bucketPath); err != nil {
		return "", fmt.Errorf("rebuild content index: %w", err)
	}
	if err := s.writeFileExclusive(readyPath, nil); err != nil && !errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("mark content index ready: %w", err)
	}
	return bucketPath, nil
}

// rebuildIndex performs a one-time migration of buckets written by versions
// that predate the persistent index. Normal creates never scan object data.
func (s *Disk) rebuildIndex(bucketPath string) error {
	directory, err := s.fs.Open(bucketPath)
	if err != nil {
		return err
	}
	entries, err := directory.ReadDir(-1)
	closeErr := directory.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("object %q is not a regular file", entry.Name())
		}
		if _, err := decodeName(entry.Name()); err != nil {
			return fmt.Errorf("decode object ID %q: %w", entry.Name(), err)
		}
		data, err := s.readRegularFile(filepath.Join(bucketPath, entry.Name()))
		if err != nil {
			return err
		}
		indexPath := filepath.Join(bucketPath, indexDirectory, contentHash(data))
		if indexedID, err := s.readRegularFile(indexPath); err == nil {
			if string(indexedID) == entry.Name() {
				continue
			}
			return fmt.Errorf("duplicate content in objects %q and %q", indexedID, entry.Name())
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := s.writeFileExclusive(indexPath, []byte(entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func (s *Disk) existingBucket(bucket string) (string, error) {
	bucketPath := encodeName(bucket)
	info, err := s.fs.Lstat(bucketPath)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("inspect bucket directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("bucket path is not a regular directory")
	}
	return bucketPath, nil
}

func (s *Disk) regularFileExists(path string) (bool, error) {
	info, err := s.fs.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, errors.New("path is not a regular file")
	}
	return true, nil
}

func (s *Disk) readRegularFile(path string) ([]byte, error) {
	info, err := s.fs.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("path is not a regular file")
	}
	file, err := s.fs.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("path is not a regular file")
	}
	return io.ReadAll(file)
}

func (s *Disk) writeFileExclusive(path string, data []byte) error {
	file, err := s.fs.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = s.fs.Remove(path)
		}
	}()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	remove = false
	return nil
}

func contentHash(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
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
