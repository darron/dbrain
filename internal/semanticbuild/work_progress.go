package semanticbuild

const workProgressInterval = 256

// WorkProgress reports absolute processed units for one bounded semantic-build
// operation. Callers accumulate these operation-local measurements into their
// own stage-level progress.
type WorkProgress struct {
	Current int
	Total   int
}

func shouldReportWorkProgress(current, total int) bool {
	return current == total || current%workProgressInterval == 0
}
