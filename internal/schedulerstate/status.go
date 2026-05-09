package schedulerstate

import "time"

type SyncAllStatus struct {
	Enabled          bool      `json:"enabled"`
	Interval         string    `json:"interval"`
	RunOnStart       bool      `json:"run_on_start"`
	Jitter           string    `json:"jitter,omitempty"`
	Running          bool      `json:"running"`
	CurrentReason    string    `json:"current_reason,omitempty"`
	CurrentStartedAt time.Time `json:"current_started_at,omitempty"`
	LastReason       string    `json:"last_reason,omitempty"`
	LastStartedAt    time.Time `json:"last_started_at,omitempty"`
	LastFinishedAt   time.Time `json:"last_finished_at,omitempty"`
	LastStatus       string    `json:"last_status,omitempty"`
	LastError        string    `json:"last_error,omitempty"`
	NextRunAt        time.Time `json:"next_run_at,omitempty"`
}
