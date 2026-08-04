package app

import (
	"bytes"
	"strings"
	"testing"
)

func TestConfigEnvDocsListEachNotificationVariableExactlyOnce(t *testing.T) {
	var out bytes.Buffer
	writeEnvMarkdownTable(&out, configEnvSpecs())
	for _, key := range []string{
		"DBRAIN_NOTIFICATIONS_ENABLED",
		"DBRAIN_NOTIFICATIONS_REPEAT_AFTER",
		"DBRAIN_NOTIFICATIONS_BUZZ_ENABLED",
		"DBRAIN_NOTIFICATIONS_BUZZ_RELAY_URL",
		"DBRAIN_NOTIFICATIONS_BUZZ_CHANNEL_ID",
		"DBRAIN_NOTIFICATIONS_BUZZ_PRIVATE_KEY_REF",
		"DBRAIN_NOTIFICATIONS_BUZZ_ALLOW_PRIVATE_ORIGIN",
		"DBRAIN_NOTIFICATIONS_SLACK_ENABLED",
		"DBRAIN_NOTIFICATIONS_SLACK_WEBHOOK_URL_REF",
	} {
		if got := strings.Count(out.String(), "`"+key+"`"); got != 1 {
			t.Fatalf("generated environment documentation lists %s %d times, want 1", key, got)
		}
	}
}
