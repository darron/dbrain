package remote

import (
	"encoding/json"
	"testing"
	"time"

	"tailscale.com/tailcfg"
)

// Control-plane map responses may include SSH policy even when this node does
// not serve SSH. Keep their wire representation compatible with Go's decoder.
func TestTailscaleMapResponseSSHPolicyJSON(t *testing.T) {
	for _, tc := range []struct {
		name     string
		action   string
		duration time.Duration
	}{
		{name: "omitted duration", action: `{"accept":true}`},
		{name: "numeric duration", action: `{"accept":true,"sessionDuration":60000000000}`, duration: time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var response tailcfg.MapResponse
			data := `{"SSHPolicy":{"rules":[{"action":` + tc.action + `}]}}`
			if err := json.Unmarshal([]byte(data), &response); err != nil {
				t.Fatalf("decode control-plane map response: %v", err)
			}
			if response.SSHPolicy == nil || len(response.SSHPolicy.Rules) != 1 || response.SSHPolicy.Rules[0] == nil || response.SSHPolicy.Rules[0].Action == nil {
				t.Fatalf("missing SSH policy action: %+v", response.SSHPolicy)
			}
			action := response.SSHPolicy.Rules[0].Action
			if !action.Accept || action.SessionDuration != tc.duration {
				t.Fatalf("decoded action: accept=%v, duration=%v; want true, %v", action.Accept, action.SessionDuration, tc.duration)
			}
		})
	}
}
