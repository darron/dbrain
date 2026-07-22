package audit

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func ParseDuration(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if strings.HasSuffix(value, "d") {
		days, err := strconv.ParseFloat(strings.TrimSuffix(value, "d"), 64)
		if err != nil || days < 0 {
			return 0, fmt.Errorf("invalid duration %q", value)
		}
		return time.Duration(days * float64(24*time.Hour)), nil
	}
	return time.ParseDuration(value)
}
