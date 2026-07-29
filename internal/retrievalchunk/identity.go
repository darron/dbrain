package retrievalchunk

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"strings"
)

func textHash(text string) string {
	return identityHash("text", text)
}

func PreparedStreamPlanDigest(encoded []byte) string {
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func identityHash(values ...string) string {
	h := sha256.New()
	for _, value := range values {
		writeLengthPrefixed(h, value)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func identityHashContext(ctx context.Context, values ...string) (string, error) {
	h := sha256.New()
	for _, value := range values {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = h.Write(length[:])
		for start := 0; start < len(value); start += 4 << 10 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			end := min(start+(4<<10), len(value))
			_, _ = h.Write([]byte(value[start:end]))
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func parentHash(parent Parent) string {
	values := []string{"parent", ProjectionVersion, strings.TrimSpace(parent.Kind), strings.TrimSpace(parent.SourceKey), strings.TrimSpace(parent.Title), strings.TrimSpace(parent.SourceType), strings.TrimSpace(parent.Author)}
	for _, section := range parent.Sections {
		values = append(values, strings.TrimSpace(section.Key), strings.TrimSpace(section.Role), boolIdentity(section.Derived), strings.TrimSpace(section.Heading), textHash(section.Text))
	}
	return identityHash(values...)
}

func parentHashContext(ctx context.Context, parent Parent) (string, error) {
	values := []string{"parent", ProjectionVersion, strings.TrimSpace(parent.Kind), strings.TrimSpace(parent.SourceKey), strings.TrimSpace(parent.Title), strings.TrimSpace(parent.SourceType), strings.TrimSpace(parent.Author)}
	for i, section := range parent.Sections {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		sectionTextHash, err := identityHashContext(ctx, "text", section.Text)
		if err != nil {
			return "", err
		}
		values = append(values, strings.TrimSpace(section.Key), strings.TrimSpace(section.Role), boolIdentity(section.Derived), strings.TrimSpace(section.Heading), sectionTextHash)
	}
	return identityHashContext(ctx, values...)
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
