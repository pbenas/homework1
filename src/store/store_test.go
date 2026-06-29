package store

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStores(t *testing.T) {
	factories := map[string]func(*testing.T) Store{
		"memory": func(*testing.T) Store {
			return NewMemory()
		},
		"disk": func(t *testing.T) Store {
			storage, err := NewDisk(t.TempDir())
			if err != nil {
				t.Fatalf("NewDisk() error = %v", err)
			}
			return storage
		},
	}

	for name, factory := range factories {
		t.Run(name, func(t *testing.T) {
			storage := factory(t)
			original := []byte("original")

			if err := storage.Create("bucket-a", "first", original); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			original[0] = 'X'

			data, err := storage.Get("bucket-a", "first")
			if err != nil {
				t.Fatalf("Get() error = %v", err)
			}
			if string(data) != "original" {
				t.Fatalf("Get() = %q", data)
			}
			data[0] = 'X'
			data, err = storage.Get("bucket-a", "first")
			if err != nil || string(data) != "original" {
				t.Fatalf("second Get() = %q, %v", data, err)
			}

			assertConflict(t, storage.Create("bucket-a", "first", []byte("other")), "first")
			assertConflict(t, storage.Create("bucket-a", "second", []byte("original")), "first")
			if err := storage.Create("bucket-b", "second", []byte("original")); err != nil {
				t.Fatalf("cross-bucket Create() error = %v", err)
			}

			if err := storage.Delete("bucket-a", "first"); err != nil {
				t.Fatalf("Delete() error = %v", err)
			}
			if _, err := storage.Get("bucket-a", "first"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("Get() error = %v, want ErrNotFound", err)
			}
			if err := storage.Delete("bucket-a", "first"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("second Delete() error = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestConflictError(t *testing.T) {
	err := (&ConflictError{ExistingID: "original"}).Error()
	if err != `object conflicts with "original"` {
		t.Fatalf("Error() = %q", err)
	}
}

func TestNewDiskErrors(t *testing.T) {
	if _, err := NewDisk(""); err == nil {
		t.Fatal("NewDisk(\"\") error = nil")
	}

	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDisk(filepath.Join(file, "child")); err == nil {
		t.Fatal("NewDisk() with file parent error = nil")
	}
}

func TestDiskCorruptEntryAndFilesystemErrors(t *testing.T) {
	root := t.TempDir()
	storage, err := NewDisk(root)
	if err != nil {
		t.Fatal(err)
	}

	bucketPath := storage.bucketPath("bucket")
	if err := os.MkdirAll(bucketPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bucketPath, "%%%"), []byte("duplicate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := storage.Create("bucket", "new", []byte("duplicate")); err == nil {
		t.Fatal("Create() with corrupt stored ID error = nil")
	}

	blockedBucket := storage.bucketPath("blocked")
	if err := os.WriteFile(blockedBucket, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := storage.Create("blocked", "id", nil); err == nil {
		t.Fatal("Create() with bucket path file error = nil")
	}
	if _, err := storage.Get("blocked", "id"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() error = %v, want filesystem error", err)
	}
	if err := storage.Delete("blocked", "id"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete() error = %v, want filesystem error", err)
	}
}

func TestDiskNameEncoding(t *testing.T) {
	for _, value := range []string{"simple", "../unsafe", "český objekt", ""} {
		encoded := encodeName(value)
		decoded, err := decodeName(encoded)
		if err != nil {
			t.Fatalf("decodeName(%q) error = %v", encoded, err)
		}
		if !bytes.Equal([]byte(decoded), []byte(value)) {
			t.Fatalf("round trip = %q, want %q", decoded, value)
		}
	}
}

func assertConflict(t *testing.T, err error, expectedID string) {
	t.Helper()
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error = %v, want ConflictError", err)
	}
	if conflict.ExistingID != expectedID {
		t.Fatalf("ExistingID = %q, want %q", conflict.ExistingID, expectedID)
	}
}
