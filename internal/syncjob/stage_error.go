package syncjob

// StageError preserves the sync stage that produced a fatal error while
// retaining the original error text and unwrap chain.
type StageError struct {
	stage syncStageID
	cause error
}

// WrapStageError records a known sync stage without changing the cause's text.
// Unknown stages are returned unchanged rather than gaining unstable identity.
func WrapStageError(stage string, cause error) error {
	if cause == nil {
		return nil
	}
	normalized, ok := parseKnownStage(stage)
	if !ok {
		return cause
	}
	return &StageError{stage: normalized, cause: cause}
}

func (e *StageError) Error() string { return e.cause.Error() }

func (e *StageError) Unwrap() error { return e.cause }

func (e *StageError) Stage() string { return string(e.stage) }

func parseKnownStage(stage string) (syncStageID, bool) {
	stageID := syncStageID(stage)
	switch stageID {
	case syncStageAppleNotes,
		syncStageSafariTabs,
		syncStageXFrontier,
		syncStageXMedia,
		syncStageXPhotoOCR,
		syncStageGitHub,
		syncStageYouTube,
		syncStageFeeds,
		syncStageSources,
		syncStageCategorize,
		syncStageMediaArchive,
		syncStageOKFExport:
		return stageID, true
	default:
		return "", false
	}
}
