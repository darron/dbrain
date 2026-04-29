package app

type envSpec struct {
	Key         string
	ConfigPath  string
	Default     string
	Description string
}

func configEnvSpecs() []envSpec {
	return []envSpec{
		{Key: "DBRAIN_ROOT", ConfigPath: "(env only)", Default: "", Description: "CLI root override. --root wins when both are set."},
		{Key: "XDG_CONFIG_HOME", ConfigPath: "(env only)", Default: "~/.config", Description: "Base directory for default config files."},
		{Key: "XDG_DATA_HOME", ConfigPath: "(env only)", Default: "~/.local/share", Description: "Base directory for default database, vault, cache, tmp, and logs."},
		{Key: "GITHUB_TOKEN", ConfigPath: "github.token or env.GITHUB_TOKEN", Default: "", Description: "GitHub API token for importing stars."},
		{Key: "DBRAIN_SUMMARY_MODEL / SUMMARIZE_MODEL", ConfigPath: "summary.model", Default: "", Description: "Default model for summarize-backed source and answer synthesis."},
		{Key: "DBRAIN_SUMMARY_LANGUAGE / DBRAIN_OUTPUT_LANGUAGE / SUMMARIZE_LANGUAGE", ConfigPath: "summary.language", Default: "en", Description: "Output language for summaries; use auto to match source language."},
		{Key: "DBRAIN_CATEGORIZE_MODEL", ConfigPath: "categorize.model", Default: "openrouter/google/gemini-2.5-flash", Description: "Default LLM model for item categorization."},
		{Key: "DBRAIN_OCR_MODEL / DBRAIN_X_PHOTO_OCR_MODEL", ConfigPath: "ocr.model", Default: "openrouter/google/gemini-3.1-flash-lite-preview", Description: "Default model for X photo OCR."},
		{Key: "DBRAIN_OLLAMA_BASE_URL / OLLAMA_BASE_URL / OLLAMA_HOST", ConfigPath: "ollama.base_url", Default: "http://127.0.0.1:11434", Description: "Ollama endpoint for local model calls."},
		{Key: "DBRAIN_OLLAMA_API_KEY / OLLAMA_API_KEY", ConfigPath: "ollama.api_key", Default: "ollama", Description: "API key label used for Ollama-compatible local calls."},
		{Key: "OPENAI_BASE_URL", ConfigPath: "openai.base_url or env.OPENAI_BASE_URL", Default: "", Description: "OpenAI-compatible base URL used by summarize adapter when already exported."},
		{Key: "OPENAI_API_KEY", ConfigPath: "openai.api_key or env.OPENAI_API_KEY", Default: "", Description: "OpenAI-compatible API key used by summarize adapter when already exported."},
		{Key: "OPENAI_USE_CHAT_COMPLETIONS", ConfigPath: "openai.use_chat_completions or env.OPENAI_USE_CHAT_COMPLETIONS", Default: "", Description: "Forces summarize/OpenAI-compatible calls onto chat completions when set."},
		{Key: "DBRAIN_USER_AGENT", ConfigPath: "http.user_agent", Default: "dbrain/<short-sha>", Description: "User-Agent header for outbound API calls; source/web fetching keeps its own fetch headers."},
		{Key: "DBRAIN_OPENROUTER_BASE_URL / OPENROUTER_BASE_URL", ConfigPath: "openrouter.base_url", Default: "https://openrouter.ai/api/v1", Description: "OpenRouter API endpoint."},
		{Key: "DBRAIN_OPENROUTER_API_KEY / OPENROUTER_API_KEY", ConfigPath: "openrouter.api_key", Default: "", Description: "OpenRouter API key for hosted LLM/OCR/categorization calls."},
		{Key: "DBRAIN_OPENROUTER_REFERER / OPENROUTER_HTTP_REFERER", ConfigPath: "openrouter.referer", Default: "https://local.dbrain", Description: "HTTP referer sent to OpenRouter for direct calls."},
		{Key: "DBRAIN_OPENROUTER_TITLE / OPENROUTER_X_TITLE", ConfigPath: "openrouter.title", Default: "dbrain", Description: "HTTP title sent to OpenRouter for direct calls."},
		{Key: "DBRAIN_SOURCE_READER_DOMAINS / DBRAIN_HTTP_READER_DOMAINS", ConfigPath: "source.reader.domains", Default: "canada.ca", Description: "Comma-separated domains routed through the reader/textifier path before summarize."},
		{Key: "DBRAIN_SOURCE_READER_BASE_URL / DBRAIN_HTTP_READER_BASE_URL", ConfigPath: "source.reader.base_url", Default: "https://r.jina.ai/", Description: "Reader/textifier base URL for difficult domains."},
		{Key: "DBRAIN_TSNET_WEB", ConfigPath: "tsnet.web", Default: "true", Description: "Mount the read/write web UI when using serve remote."},
		{Key: "DBRAIN_TSNET_MCP", ConfigPath: "tsnet.mcp", Default: "true", Description: "Mount the read-only MCP HTTP endpoint when using serve remote."},
		{Key: "DBRAIN_TSNET_MCP_PATH", ConfigPath: "tsnet.mcp_path", Default: "/mcp", Description: "Remote MCP endpoint path; must be a clean absolute path."},
		{Key: "DBRAIN_TSNET_HOSTNAME", ConfigPath: "tsnet.hostname", Default: "dbrain", Description: "Stable tailnet machine name for the built-in tsnet node."},
		{Key: "DBRAIN_TSNET_STATE_DIR", ConfigPath: "tsnet.state_dir", Default: "<data_dir>/tsnet/<hostname>", Description: "Durable tsnet state directory; must not be synced via iCloud/Dropbox/etc."},
		{Key: "DBRAIN_TSNET_LISTEN", ConfigPath: "tsnet.listen", Default: ":443", Description: "Tailnet listen address for serve remote; default changes to :80 when TLS is disabled."},
		{Key: "DBRAIN_TSNET_TLS", ConfigPath: "tsnet.tls", Default: "true", Description: "Use Tailscale HTTPS through tsnet ListenTLS."},
		{Key: "DBRAIN_TSNET_STARTUP_TIMEOUT", ConfigPath: "tsnet.startup_timeout", Default: "45s", Description: "Maximum time to wait for tsnet startup and authentication."},
		{Key: "DBRAIN_TSNET_AUTH_KEY", ConfigPath: "tsnet.auth_key", Default: "", Description: "Optional direct Tailscale auth key; prefer a typed secret reference."},
		{Key: "DBRAIN_TSNET_AUTH_KEY_REF", ConfigPath: "tsnet.auth_key_ref", Default: "", Description: "Typed auth key reference: env:, op://, or keychain://."},
		{Key: "DBRAIN_TSNET_ALLOW_SECRET_COMMAND", ConfigPath: "tsnet.allow_secret_command", Default: "false", Description: "Permit YAML-only tsnet.auth_key_command execution."},
		{Key: "DBRAIN_TSNET_ADVERTISE_TAGS", ConfigPath: "tsnet.advertise_tags", Default: "", Description: "Comma-separated Tailscale tags to request for the tsnet node."},
		{Key: "DBRAIN_TSNET_CONTROL_URL", ConfigPath: "tsnet.control_url", Default: "", Description: "Experimental alternate Tailscale control server URL."},
		{Key: "DBRAIN_TSNET_VERBOSE", ConfigPath: "tsnet.verbose", Default: "false", Description: "Enable verbose tsnet backend logs."},
		{Key: "DBRAIN_MEDIA_PROXY_BASE_URL / DBRAIN_WEB_BASE_URL", ConfigPath: "media.proxy.base_url", Default: "http://127.0.0.1:8742", Description: "Base URL for local archived-media proxy links in rendered notes."},
		{Key: "DBRAIN_AUTO_ARCHIVE_MEDIA / DBRAIN_ARCHIVE_AUTO", ConfigPath: "archive.auto", Default: "false", Description: "Run media archive automatically at the end of sync all."},
		{Key: "DBRAIN_ARCHIVE_UPLOAD / DBRAIN_R2_UPLOAD", ConfigPath: "archive.upload", Default: "false", Description: "Upload eligible media before marking/pruning in archive media."},
		{Key: "DBRAIN_ARCHIVE_PROVIDER / DBRAIN_R2_PROVIDER", ConfigPath: "archive.provider", Default: "cloudflare_r2", Description: "Archive provider label."},
		{Key: "DBRAIN_R2_BUCKET / DBRAIN_ARCHIVE_BUCKET / DBRAIN_S3_BUCKET", ConfigPath: "r2.bucket or archive.bucket", Default: "", Description: "S3-compatible bucket for media and SQLite archives."},
		{Key: "DBRAIN_R2_PUBLIC_BASE_URL / DBRAIN_MEDIA_PUBLIC_BASE_URL", ConfigPath: "r2.public_base_url or media.public_base_url", Default: "", Description: "Public base URL for archived media links."},
		{Key: "DBRAIN_R2_ENDPOINT / DBRAIN_S3_ENDPOINT", ConfigPath: "r2.endpoint", Default: "", Description: "S3-compatible endpoint, such as a Cloudflare R2 account endpoint."},
		{Key: "DBRAIN_R2_REGION / DBRAIN_S3_REGION / AWS_REGION / AWS_DEFAULT_REGION", ConfigPath: "r2.region", Default: "auto", Description: "S3-compatible region."},
		{Key: "DBRAIN_R2_ACCESS_KEY_ID / DBRAIN_S3_ACCESS_KEY_ID / AWS_ACCESS_KEY_ID", ConfigPath: "r2.access_key_id", Default: "", Description: "S3-compatible access key ID."},
		{Key: "DBRAIN_R2_SECRET_ACCESS_KEY / DBRAIN_S3_SECRET_ACCESS_KEY / AWS_SECRET_ACCESS_KEY", ConfigPath: "r2.secret_access_key", Default: "", Description: "S3-compatible secret access key."},
		{Key: "DBRAIN_R2_SESSION_TOKEN / DBRAIN_S3_SESSION_TOKEN / AWS_SESSION_TOKEN", ConfigPath: "r2.session_token", Default: "", Description: "Optional S3-compatible session token."},
	}
}
