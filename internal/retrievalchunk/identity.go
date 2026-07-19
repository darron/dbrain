package retrievalchunk

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"strconv"
)

func textHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func chunkID(parent Parent, role string, ordinal int, chunkTextHash string) string {
	h := sha256.New()
	writeLengthPrefixed(h, parent.Kind)
	writeLengthPrefixed(h, parent.SourceKey)
	writeLengthPrefixed(h, role)
	writeLengthPrefixed(h, parent.ContentHash)
	writeLengthPrefixed(h, Version)
	writeLengthPrefixed(h, strconv.Itoa(ordinal))
	writeLengthPrefixed(h, chunkTextHash)
	return hex.EncodeToString(h.Sum(nil))
}

func writeLengthPrefixed(h hash.Hash, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = h.Write(length[:])
	_, _ = h.Write([]byte(value))
}
