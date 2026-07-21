package retrievalchunk

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"strings"
)

func textHash(text string) string {
	return identityHash("text", text)
}

func identityHash(values ...string) string {
	h := sha256.New()
	for _, value := range values {
		writeLengthPrefixed(h, value)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func parentHash(parent Parent) string {
	values := []string{"parent", ProjectionVersion, strings.TrimSpace(parent.Kind), strings.TrimSpace(parent.SourceKey), strings.TrimSpace(parent.ContentHash), strings.TrimSpace(parent.Title), strings.TrimSpace(parent.SourceType), strings.TrimSpace(parent.Author)}
	for _, section := range parent.Sections {
		values = append(values, strings.TrimSpace(section.Key), strings.TrimSpace(section.Role), boolIdentity(section.Derived), strings.TrimSpace(section.Heading), textHash(section.Text))
	}
	return identityHash(values...)
}

func sectionKey(parent Parent, section Section) string {
	if key := strings.TrimSpace(section.Key); key != "" {
		return identityHash("section", ProjectionVersion, strings.TrimSpace(parent.Kind), strings.TrimSpace(parent.SourceKey), key)
	}
	return identityHash("section", ProjectionVersion, strings.TrimSpace(parent.Kind), strings.TrimSpace(parent.SourceKey), strings.TrimSpace(section.Role), boolIdentity(section.Derived), strings.TrimSpace(section.Heading))
}

func headingHash(heading string) string { return identityHash("heading", strings.TrimSpace(heading)) }

func chunkID(sectionKey, role string, derived bool, headingHash, chunkTextHash string) string {
	return identityHash("chunk", ProjectionVersion, Version, sectionKey, strings.TrimSpace(role), boolIdentity(derived), headingHash, chunkTextHash)
}

func boolIdentity(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func writeLengthPrefixed(h hash.Hash, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = h.Write(length[:])
	_, _ = h.Write([]byte(value))
}
