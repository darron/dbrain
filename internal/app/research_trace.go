package app

import (
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/researchtrace"
)

func writeResearchTrace(cfg config.Config, recorder *researchtrace.Recorder, keepAll bool) (string, error) {
	if recorder == nil {
		return "", nil
	}
	trace, artifacts := recorder.Snapshot()
	result, err := researchtrace.Write(cfg, trace, artifacts, researchtrace.WriteOptions{
		Retention: researchtrace.RetentionOptions{KeepAll: keepAll},
	})
	if err != nil {
		return "", err
	}
	return result.RelativePath, nil
}
