package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	indexDirectory = ".index"
	indexReadyFile = ".ready"
)

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
	entries, readErr := directory.ReadDir(-1)
	closeErr := directory.Close()
	if readErr != nil {
		return readErr
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
		indexPath := filepath.Join(bucketPath, indexDirectory, digestContent(data).String())
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
