package util

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
)

// FileDigest computes the SHA-256 hex digest of a file's contents.
func FileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ContentDigest computes the SHA-256 hex digest of a byte slice.
func ContentDigest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
