//go:build !(aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris)

package store

import (
	"context"
	"errors"
)

func diskLockSupported() bool {
	return false
}

func (s *Disk) withBucketLock(context.Context, string, bucketLockMode, func() error) error {
	return errors.New("disk backend is unsupported on this operating system")
}
