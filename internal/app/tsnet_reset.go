package app

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

func confirmTSNetReset(in io.Reader, out io.Writer, hostname string, stateDir string) (bool, error) {
	_, _ = fmt.Fprintf(out, "Remove tsnet state directory?\n")
	_, _ = fmt.Fprintf(out, "Hostname: %s\n", hostname)
	_, _ = fmt.Fprintf(out, "State dir: %s\n", stateDir)
	_, _ = fmt.Fprintf(out, "This will require dbrain to authenticate with Tailscale again.\n")
	_, _ = fmt.Fprintf(out, "Type 'reset' to continue: ")

	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, err
		}
		return false, nil
	}
	return strings.TrimSpace(scanner.Text()) == "reset", nil
}
