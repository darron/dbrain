package retrievalchunk

import (
	"fmt"
	"strings"
)

// synthetic26512WindowFixture is intentionally synthetic; it models a large
// restored corpus without placing any private corpus text in the repository.
func synthetic26512WindowFixture() string {
	parts := make([]string, 26_512)
	for i := range parts {
		parts[i] = fmt.Sprintf("window-%06d stable semantic evidence.", i)
	}
	return strings.Join(parts, "\n\n")
}
