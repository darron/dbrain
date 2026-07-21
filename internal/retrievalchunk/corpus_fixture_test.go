package retrievalchunk

import "fmt"

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
	return repeatedSyntheticToken(7_000)
}
func repeatedSyntheticToken(n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += fmt.Sprintf("token%05d", i%997)
	}
	return result
}
