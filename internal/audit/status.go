package audit

func Overall(checks []Check) (Status, Confidence) {
	status := StatusPass
	confidence := ConfidenceHigh
	required := 0
	for _, check := range checks {
		if !check.Required || check.Status == StatusSkipped {
			continue
		}
		required++
		if statusRank(check.Status) > statusRank(status) {
			status = check.Status
		}
		if confidenceRank(check.Confidence) > confidenceRank(confidence) {
			confidence = check.Confidence
		}
	}
	if required == 0 {
		return StatusUnknown, ConfidenceUnknown
	}
	return status, confidence
}

func ExitCode(report Report) int {
	switch report.Status {
	case StatusPass:
		return 0
	case StatusWarn:
		return 1
	case StatusFail:
		return 2
	default:
		return 3
	}
}

func statusRank(status Status) int {
	switch status {
	case StatusPass:
		return 0
	case StatusWarn:
		return 1
	case StatusUnknown:
		return 2
	case StatusFail:
		return 3
	default:
		return -1
	}
}

func confidenceRank(confidence Confidence) int {
	switch confidence {
	case ConfidenceHigh:
		return 0
	case ConfidenceModerate:
		return 1
	case ConfidenceLow:
		return 2
	default:
		return 3
	}
}
