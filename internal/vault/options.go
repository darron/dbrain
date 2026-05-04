package vault

import (
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/runtimeenv"
)

const defaultMediaProxyBaseURL = "http://127.0.0.1:8742"

func renderOptionsForConfig(cfg config.Config) RenderOptions {
	baseURL := strings.TrimSpace(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_MEDIA_PROXY_BASE_URL", "DBRAIN_WEB_BASE_URL"))
	switch strings.ToLower(baseURL) {
	case "off", "none", "disabled":
		return RenderOptions{}
	}
	if baseURL == "" {
		baseURL = defaultMediaProxyBaseURL
	}
	return RenderOptions{MediaProxyBaseURL: baseURL}
}
