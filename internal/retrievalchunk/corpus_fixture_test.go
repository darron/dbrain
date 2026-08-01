package retrievalchunk

import (
	"fmt"
)

// synthetic26512WindowFixture is intentionally synthetic; it models a large
// restored corpus without placing any private corpus text in the repository.
func synthetic26512WindowFixture() []Section {
	sections := make([]Section, 26_512)
	for i := range sections {
		sections[i] = Section{Key: fmt.Sprintf("synthetic-%06d", i), Role: "raw", Heading: "Synthetic", Text: fmt.Sprintf("window-%06d stable semantic evidence.", i)}
	}
	return sections
}

func syntheticUnstructuredFixture() string {
	const size = 20_000
	result := make([]byte, size)
	state := uint32(0x5eed1234)
	for i := range result {
		// Deterministic xorshift bytes provide unstructured valid UTF-8 without
		// introducing natural paragraph, sentence, or whitespace boundaries.
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		result[i] = 'a' + byte(state%26)
	}
	return string(result)
}
