package store

type bucketLockMode uint8

const (
	sharedLock bucketLockMode = iota
	exclusiveLock
)
