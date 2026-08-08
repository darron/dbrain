package bskyapi

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

var safeNoteRKey = regexp.MustCompile(`[^A-Za-z0-9._~-]+`)

func noteIDForPostURI(rawURI string) (string, error) {
	rawURI = strings.TrimSpace(rawURI)
	if !strings.HasPrefix(rawURI, "at://") {
		return "", fmt.Errorf("invalid Bluesky post URI")
	}
	rest := strings.TrimPrefix(rawURI, "at://")
	separator := strings.IndexByte(rest, '/')
	if separator <= 0 {
		return "", fmt.Errorf("invalid Bluesky post URI")
	}
	authority := rest[:separator]
	if !strings.HasPrefix(authority, "did:") {
		return "", fmt.Errorf("invalid Bluesky post URI")
	}
	parts := strings.Split(strings.Trim(rest[separator:], "/"), "/")
	if len(parts) != 2 || parts[0] != "app.bsky.feed.post" || parts[1] == "" {
		return "", fmt.Errorf("invalid Bluesky post URI")
	}

	rkey := safeNoteRKey.ReplaceAllString(parts[1], "-")
	rkey = strings.Trim(rkey, "-")
	if rkey == "" {
		rkey = "post"
	}
	digest := sha256.Sum256([]byte(rawURI))
	return fmt.Sprintf("%s-%s", rkey, hex.EncodeToString(digest[:])[:12]), nil
}
