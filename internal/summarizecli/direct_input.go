package summarizecli

import (
	"fmt"
	"os"
	"strings"
)

func localSummaryInput(opts Options) (string, bool, error) {
	if !opts.Summarize {
		return "", false, nil
	}
	if value := strings.TrimSpace(opts.Stdin); value != "" {
		return value, true, nil
	}
	input := strings.TrimSpace(opts.Input)
	if input == "" {
		return "", false, nil
	}
	info, err := os.Stat(input)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("stat summary input: %w", err)
	}
	if info.IsDir() {
		return "", false, nil
	}
	data, err := os.ReadFile(input)
	if err != nil {
		return "", false, fmt.Errorf("read summary input: %w", err)
	}
	return string(data), true, nil
}
