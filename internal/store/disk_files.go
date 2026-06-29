package store

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

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
