package audit

import "testing"

func TestOverallUsesRequiredPrecedenceAndWeakestConfidence(t *testing.T) {
	tests := []struct {
		name       string
		checks     []Check
		status     Status
		confidence Confidence
		exit       int
	}{
		{"pass", []Check{{Required: true, Status: StatusPass, Confidence: ConfidenceHigh}}, StatusPass, ConfidenceHigh, 0},
		{"warn", []Check{{Required: true, Status: StatusPass, Confidence: ConfidenceHigh}, {Required: true, Status: StatusWarn, Confidence: ConfidenceModerate}}, StatusWarn, ConfidenceModerate, 1},
		{"unknown beats warn", []Check{{Required: true, Status: StatusWarn, Confidence: ConfidenceHigh}, {Required: true, Status: StatusUnknown, Confidence: ConfidenceUnknown}}, StatusUnknown, ConfidenceUnknown, 3},
		{"fail beats unknown", []Check{{Required: true, Status: StatusUnknown, Confidence: ConfidenceUnknown}, {Required: true, Status: StatusFail, Confidence: ConfidenceHigh}}, StatusFail, ConfidenceUnknown, 2},
		{"optional ignored", []Check{{Required: true, Status: StatusPass, Confidence: ConfidenceHigh}, {Required: false, Status: StatusFail, Confidence: ConfidenceLow}}, StatusPass, ConfidenceHigh, 0},
		{"skipped ignored", []Check{{Required: false, Status: StatusSkipped, Confidence: ConfidenceUnknown}}, StatusUnknown, ConfidenceUnknown, 3},
		{"zero required", nil, StatusUnknown, ConfidenceUnknown, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, confidence := Overall(tt.checks)
			if status != tt.status || confidence != tt.confidence {
				t.Fatalf("Overall = %s/%s, want %s/%s", status, confidence, tt.status, tt.confidence)
			}
			r := Report{Status: status}
			if got := ExitCode(r); got != tt.exit {
				t.Fatalf("ExitCode = %d, want %d", got, tt.exit)
			}
		})
	}
}
