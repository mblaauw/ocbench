// Package id generates the random identifiers ocbench persists. One
// implementation keeps the format identical everywhere a new row is created.
package id

import (
	"crypto/rand"
	"fmt"
)

// NewUUID returns a random RFC 4122 version 4 identifier formatted as
// 8-4-4-4-12 hex. It needs no external dependency.
func NewUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
