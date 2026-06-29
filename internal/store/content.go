package store

import (
	"crypto/sha256"
	"encoding/hex"
)

type contentDigest [sha256.Size]byte

func digestContent(data []byte) contentDigest {
	return contentDigest(sha256.Sum256(data))
}

func (digest contentDigest) String() string {
	return hex.EncodeToString(digest[:])
}
