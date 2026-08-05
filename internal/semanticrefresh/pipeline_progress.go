package semanticrefresh

import (
	"fmt"
	"math"
	"sync"

	"github.com/darron/dbrain/internal/store"
)

type pipelineProgressTracker struct {
	mu          sync.Mutex
	runID       string
	stage       store.SemanticRefreshStage
	initialized bool
	committed   StageWork
}

func (t *pipelineProgressTracker) begin(run store.SemanticRefreshRun, remaining int64, totalKnown bool) (StageWork, error) {
	if remaining < 0 {
		return StageWork{}, fmt.Errorf("semantic stage remaining work must not be negative")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.runID != run.RunID || t.stage != run.Stage {
		t.runID = run.RunID
		t.stage = run.Stage
		t.initialized = false
		t.committed = StageWork{}
	}
	if !t.initialized {
		t.initialized = true
		t.committed = StageWork{Total: remaining, TotalKnown: totalKnown}
	} else if totalKnown {
		if remaining > math.MaxInt64-t.committed.Current {
			return StageWork{}, fmt.Errorf("semantic stage work total overflow")
		}
		candidate := t.committed.Current + remaining
		if !t.committed.TotalKnown || candidate > t.committed.Total {
			t.committed.Total = candidate
		}
		t.committed.TotalKnown = true
	}
	return t.committed, nil
}

func (t *pipelineProgressTracker) commit(run store.SemanticRefreshRun, work StageWork) error {
	if err := work.Validate(); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.runID != run.RunID || t.stage != run.Stage || !t.initialized {
		return fmt.Errorf("semantic stage progress tracker changed before commit")
	}
	t.committed = work
	return nil
}

func advanceStageWork(base StageWork, current, total int64, totalKnown bool) (StageWork, error) {
	if current < 0 || total < 0 {
		return StageWork{}, fmt.Errorf("semantic operation work must not be negative")
	}
	work := base
	if current > math.MaxInt64-base.Current {
		return StageWork{}, fmt.Errorf("semantic stage current work overflow")
	}
	work.Current = base.Current + current
	if totalKnown {
		if total > math.MaxInt64-base.Current {
			return StageWork{}, fmt.Errorf("semantic stage total work overflow")
		}
		candidate := base.Current + total
		if !work.TotalKnown || candidate > work.Total {
			work.Total = candidate
		}
		work.TotalKnown = true
	}
	if work.TotalKnown && work.Current > work.Total {
		work.Total = work.Current
	}
	return work, work.Validate()
}

func publishStageWork(callback StageProgressCallback, work StageWork) error {
	if callback == nil {
		return nil
	}
	return callback(work)
}
