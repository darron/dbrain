package mastodonapi

// This file intentionally keeps media conversion in status.go's typed
// projection for A2. It is a separate package boundary for PR B and callers
// that need attachment policy without knowing the full status normalizer.
