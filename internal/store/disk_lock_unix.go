//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

func diskLockSupported() bool {
	return true
}

func (s *Disk) withBucketLock(ctx context.Context, bucket string, mode bucketLockMode, operation func() error) error {
	lockPath := "." + encodeName(bucket) + ".lock"
	lockDescriptor, err := unix.Open(
		filepath.Join(s.root, lockPath),
		unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW,
		0o600,
	)
	if err != nil {
		return fmt.Errorf("open bucket lock: %w", err)
	}
	lock := os.NewFile(uintptr(lockDescriptor), lockPath)
	defer lock.Close()

	lockMode := unix.LOCK_SH
	if mode == exclusiveLock {
		lockMode = unix.LOCK_EX
	}
	if err := flockContext(ctx, int(lock.Fd()), lockMode); err != nil {
		return fmt.Errorf("lock bucket: %w", err)
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN) //nolint:errcheck
	if err := ctx.Err(); err != nil {
		return err
	}
	return operation()
}

func flockContext(ctx context.Context, descriptor, mode int) error {
	const retryInterval = 10 * time.Millisecond
	for {
		err := unix.Flock(descriptor, mode|unix.LOCK_NB)
		if err == nil {
			return nil
		}
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			return err
		}
		timer := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
