package store

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func testHashText(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}
