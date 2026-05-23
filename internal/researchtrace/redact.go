package researchtrace

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/darron/dbrain/internal/config"
)

var (
	bearerTokenRE = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{8,}`)
	dbrainTokenRE = regexp.MustCompile(`\bdbrain_[A-Za-z0-9._~+/=-]{12,}`)
	apiKeyRE      = regexp.MustCompile(`\b(sk|ork|or)-[A-Za-z0-9._~+/=-]{12,}`)
	envSecretRE   = regexp.MustCompile(`(?i)\b([A-Z0-9_]*(TOKEN|SECRET|API_KEY|ACCESS_KEY|SESSION_KEY)[A-Z0-9_]*\s*[:=]\s*)[^"'\s]+`)
	absPathRE     = regexp.MustCompile(`/[A-Za-z0-9._~+@%=-][^ \n\t"')\]]*`)
)

func redactText(cfg config.Config, text string) string {
	if text == "" {
		return ""
	}
	out := bearerTokenRE.ReplaceAllString(text, "Bearer [REDACTED]")
	out = dbrainTokenRE.ReplaceAllString(out, "[REDACTED_TOKEN]")
	out = apiKeyRE.ReplaceAllString(out, "[REDACTED_KEY]")
	out = envSecretRE.ReplaceAllString(out, `${1}[REDACTED]`)
	out = redactPaths(cfg, out)
	return out
}

func redactPaths(cfg config.Config, text string) string {
	tempRoot := cleanPath(cfg.TempDir)
	allowed := []string{
		cleanPath(cfg.RootDir),
		cleanPath(cfg.ConfigDir),
		cleanPath(cfg.DataDir),
		cleanPath(cfg.VaultDir),
		cleanPath(cfg.MediaDir),
		cleanPath(cfg.CacheDir),
		cleanPath(cfg.LogDir),
	}
	matches := absPathRE.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return text
	}
	var b strings.Builder
	last := 0
	for _, match := range matches {
		start, end := match[0], match[1]
		value := text[start:end]
		b.WriteString(text[last:start])
		last = end
		if start > 0 && (text[start-1] == ':' || text[start-1] == '/') {
			b.WriteString(value)
			continue
		}
		cleaned := cleanPath(value)
		if cleaned == "" {
			b.WriteString(value)
			continue
		}
		if tempRoot != "" && withinPath(cleaned, tempRoot) {
			b.WriteString("[dbrain-temp]")
			continue
		}
		allowedPath := false
		for _, root := range allowed {
			if root == "" {
				continue
			}
			if withinPath(cleaned, root) {
				allowedPath = true
				break
			}
		}
		if allowedPath {
			b.WriteString(value)
		} else {
			b.WriteString("[redacted-path]")
		}
	}
	b.WriteString(text[last:])
	return b.String()
}

func cleanPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !filepath.IsAbs(value) {
		return ""
	}
	return filepath.Clean(value)
}

func withinPath(path string, root string) bool {
	if path == root {
		return true
	}
	prefix := root
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(path, prefix)
}
