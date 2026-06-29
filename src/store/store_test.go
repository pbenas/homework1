package store

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
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

func TestMemoryHashIndexIsCollisionSafeAndMaintained(t *testing.T) {
	storage := NewMemory()
	target := []byte("target")
	hash := sha256.Sum256(target)
	storage.buckets["bucket"] = &memoryBucket{
		byID: map[string]memoryObject{
			"collision": {data: []byte("different"), hash: hash},
		},
		byHash: map[[sha256.Size]byte][]string{
			hash: {"collision"},
		},
	}

	if err := storage.Create("bucket", "original", target); err != nil {
		t.Fatalf("Create() with simulated hash collision error = %v", err)
	}
	if IDs := storage.buckets["bucket"].byHash[hash]; len(IDs) != 2 {
		t.Fatalf("hash index IDs = %v, want two collision candidates", IDs)
	}
	assertConflict(t, storage.Create("bucket", "duplicate", target), "original")

	if err := storage.Delete("bucket", "original"); err != nil {
		t.Fatal(err)
	}
	if IDs := storage.buckets["bucket"].byHash[hash]; len(IDs) != 1 || IDs[0] != "collision" {
		t.Fatalf("hash index after delete = %v", IDs)
	}
	if err := storage.Delete("bucket", "collision"); err != nil {
		t.Fatal(err)
	}
	if _, exists := storage.buckets["bucket"]; exists {
		t.Fatal("empty bucket and hash index were not removed")
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

func TestDiskClose(t *testing.T) {
	storage, err := NewDisk(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := storage.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if err := storage.Create("bucket", "id", []byte("data")); err == nil {
		t.Fatal("Create() after Close() error = nil")
	}
}

func TestDiskFilesystemErrors(t *testing.T) {
	root := t.TempDir()
	storage, err := NewDisk(root)
	if err != nil {
		t.Fatal(err)
	}

	bucketPath := storage.bucketPath("bucket")
	if err := os.MkdirAll(bucketPath, 0o700); err != nil {
		t.Fatal(err)
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

func TestDiskPersistentIndex(t *testing.T) {
	root := t.TempDir()
	first, err := NewDisk(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Create("bucket", "original", []byte("same")); err != nil {
		t.Fatal(err)
	}
	second, err := NewDisk(root)
	if err != nil {
		t.Fatal(err)
	}
	assertConflict(t, second.Create("bucket", "duplicate", []byte("same")), "original")
	if err := second.Delete("bucket", "original"); err != nil {
		t.Fatal(err)
	}
	if err := first.Create("bucket", "replacement", []byte("same")); err != nil {
		t.Fatalf("Create() after delete error = %v", err)
	}
}

func TestDiskRejectsCorruptContentIndex(t *testing.T) {
	root := t.TempDir()
	storage, err := NewDisk(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Create("bucket", "original", []byte("same")); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(storage.bucketPath("bucket"), indexDirectory, contentHash([]byte("same")))
	if err := os.WriteFile(indexPath, []byte("../outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := storage.Create("bucket", "duplicate", []byte("same")); err == nil {
		t.Fatal("Create() with corrupt content index error = nil")
	}
}

func TestDiskMigratesLegacyBucketAndRecoversStaleIndex(t *testing.T) {
	root := t.TempDir()
	bucketPath := filepath.Join(root, encodeName("bucket"))
	if err := os.Mkdir(bucketPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bucketPath, encodeName("legacy")), []byte("same"), 0o600); err != nil {
		t.Fatal(err)
	}
	storage, err := NewDisk(root)
	if err != nil {
		t.Fatal(err)
	}
	assertConflict(t, storage.Create("bucket", "duplicate", []byte("same")), "legacy")

	staleData := []byte("interrupted")
	staleIndex := filepath.Join(bucketPath, indexDirectory, contentHash(staleData))
	if err := os.WriteFile(staleIndex, []byte(encodeName("missing")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := storage.Create("bucket", "recovered", staleData); err != nil {
		t.Fatalf("Create() with stale index error = %v", err)
	}
}

func TestDiskResumesInterruptedIndexMigration(t *testing.T) {
	root := t.TempDir()
	bucketPath := filepath.Join(root, encodeName("bucket"))
	indexPath := filepath.Join(bucketPath, indexDirectory)
	if err := os.MkdirAll(indexPath, 0o700); err != nil {
		t.Fatal(err)
	}
	for id, data := range map[string]string{"first": "one", "second": "two"} {
		if err := os.WriteFile(filepath.Join(bucketPath, encodeName(id)), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate termination after only the first legacy object was indexed.
	if err := os.WriteFile(filepath.Join(indexPath, contentHash([]byte("one"))), []byte(encodeName("first")), 0o600); err != nil {
		t.Fatal(err)
	}

	storage, err := NewDisk(root)
	if err != nil {
		t.Fatal(err)
	}
	assertConflict(t, storage.Create("bucket", "duplicate", []byte("two")), "second")
	if info, err := os.Lstat(filepath.Join(indexPath, indexReadyFile)); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("migration marker info=%v error=%v", info, err)
	}
}

func TestDiskRejectsCorruptLegacyBuckets(t *testing.T) {
	for name, entries := range map[string]map[string]string{
		"invalid object name": {"%%%": "data"},
		"duplicate content": {
			encodeName("first"):  "same",
			encodeName("second"): "same",
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			bucketPath := filepath.Join(root, encodeName("bucket"))
			if err := os.Mkdir(bucketPath, 0o700); err != nil {
				t.Fatal(err)
			}
			for filename, data := range entries {
				if err := os.WriteFile(filepath.Join(bucketPath, filename), []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			storage, err := NewDisk(root)
			if err != nil {
				t.Fatal(err)
			}
			if err := storage.Create("bucket", "new", []byte("new")); err == nil {
				t.Fatal("Create() with corrupt legacy bucket error = nil")
			}
		})
	}
}

func TestDiskConcurrentInstancesDeduplicate(t *testing.T) {
	root := t.TempDir()
	const instances = 12
	stores := make([]*Disk, instances)
	for i := range stores {
		var err error
		stores[i], err = NewDisk(root)
		if err != nil {
			t.Fatal(err)
		}
	}

	start := make(chan struct{})
	errs := make(chan error, instances)
	var wg sync.WaitGroup
	for i, storage := range stores {
		wg.Add(1)
		go func(i int, storage *Disk) {
			defer wg.Done()
			<-start
			errs <- storage.Create("bucket", string(rune('a'+i)), []byte("shared content"))
		}(i, storage)
	}
	close(start)
	wg.Wait()
	close(errs)

	created, conflicted := 0, 0
	for err := range errs {
		if err == nil {
			created++
			continue
		}
		var conflict *ConflictError
		if errors.As(err, &conflict) {
			conflicted++
			continue
		}
		t.Fatalf("unexpected Create() error = %v", err)
	}
	if created != 1 || conflicted != instances-1 {
		t.Fatalf("created=%d conflicted=%d", created, conflicted)
	}
}

func TestDiskBucketLockAllowsReadersAndBlocksWriter(t *testing.T) {
	storage, err := NewDisk(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()

	readerRelease := make(chan struct{})
	readerEntered := make(chan struct{}, 2)
	errors := make(chan error, 3)
	for range 2 {
		go func() {
			errors <- storage.withBucketLock("bucket", false, func() error {
				readerEntered <- struct{}{}
				<-readerRelease
				return nil
			})
		}()
	}
	for range 2 {
		select {
		case <-readerEntered:
		case <-time.After(time.Second):
			t.Fatal("shared bucket locks did not run concurrently")
		}
	}

	writerEntered := make(chan struct{})
	writerRelease := make(chan struct{})
	go func() {
		errors <- storage.withBucketLock("bucket", true, func() error {
			close(writerEntered)
			<-writerRelease
			return nil
		})
	}()
	select {
	case <-writerEntered:
		t.Fatal("exclusive bucket lock entered while readers were active")
	case <-time.After(100 * time.Millisecond):
	}

	close(readerRelease)
	select {
	case <-writerEntered:
	case <-time.After(time.Second):
		t.Fatal("exclusive bucket lock did not enter after readers exited")
	}
	close(writerRelease)
	for range 3 {
		if err := <-errors; err != nil {
			t.Fatalf("bucket lock error = %v", err)
		}
	}
}

func TestDiskRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	storage, err := NewDisk(root)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(outside, storage.bucketPath("bucket")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := storage.Create("bucket", "id", []byte("secret")); err == nil {
		t.Fatal("Create() through bucket symlink error = nil")
	}
	if _, err := storage.Get("bucket", "id"); err == nil {
		t.Fatal("Get() through bucket symlink error = nil")
	}

	if err := os.Remove(storage.bucketPath("bucket")); err != nil {
		t.Fatal(err)
	}
	if err := storage.Create("bucket", "safe", []byte("safe")); err != nil {
		t.Fatal(err)
	}
	objectPath := storage.objectPath("bucket", "linked")
	outsideFile := filepath.Join(outside, "secret")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideFile, objectPath); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Get("bucket", "linked"); err == nil {
		t.Fatal("Get() through object symlink error = nil")
	}
	if err := storage.Delete("bucket", "linked"); err == nil {
		t.Fatal("Delete() through object symlink error = nil")
	}

	indexPath := filepath.Join(storage.bucketPath("index-symlink"), indexDirectory)
	if err := os.Mkdir(filepath.Dir(indexPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, indexPath); err != nil {
		t.Fatal(err)
	}
	if err := storage.Create("index-symlink", "id", []byte("data")); err == nil {
		t.Fatal("Create() through index symlink error = nil")
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
