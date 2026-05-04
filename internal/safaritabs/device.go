package safaritabs

import (
	"fmt"
	"strings"
)

func findDevice(devices []Device, device string) (Device, bool) {
	needle := strings.ToLower(strings.TrimSpace(device))
	for _, candidate := range devices {
		if strings.ToLower(candidate.Name) == needle || strings.ToLower(candidate.UUID) == needle {
			return candidate, true
		}
	}
	return Device{}, false
}

func formatDeviceNames(devices []Device) string {
	if len(devices) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(devices))
	for _, device := range devices {
		name := strings.TrimSpace(device.Name)
		if name == "" {
			name = device.UUID
		}
		parts = append(parts, fmt.Sprintf("%s (%d tabs)", name, device.TabCount))
	}
	return strings.Join(parts, ", ")
}
