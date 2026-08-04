package app

import "time"

type scheduledSyncStatus string

const (
	scheduledSyncStatusOK        scheduledSyncStatus = "ok"
	scheduledSyncStatusError     scheduledSyncStatus = "error"
	scheduledSyncStatusCancelled scheduledSyncStatus = "cancelled"
)

type scheduledSyncOutcome struct {
	Reason     string
	Status     scheduledSyncStatus
	StartedAt  time.Time
	FinishedAt time.Time
	Err        error
}
