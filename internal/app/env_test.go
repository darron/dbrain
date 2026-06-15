package app

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	scrubAmbientAppTestEnv()
	os.Exit(m.Run())
}

func scrubAmbientAppTestEnv() {
	for _, key := range []string{
		"DBRAIN_CONFIG_FILE",
		"DBRAIN_ROOT",
		"DBRAIN_TSNET_AUTH_KEY",
		"DBRAIN_TSNET_AUTH_KEY_REF",
		"DBRAIN_TSNET_CONTROL_URL",
		"DBRAIN_TSNET_FUNNEL",
		"DBRAIN_TSNET_HOSTNAME",
		"DBRAIN_TSNET_LISTEN",
		"DBRAIN_TSNET_MCP",
		"DBRAIN_TSNET_MCP_PATH",
		"DBRAIN_TSNET_STATE_DIR",
		"DBRAIN_TSNET_TLS",
		"DBRAIN_TSNET_WEB",
	} {
		_ = os.Unsetenv(key)
	}
}
