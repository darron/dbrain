package itemcategorize

import "testing"

func TestMergeUserTagsPreservesExistingAndDedupesGenerated(t *testing.T) {
	result := Result{
		Tags:       []string{"canada", "public-safety", "canada"},
		Categories: []string{"Canadian Politics", "public-safety"},
	}

	got := MergeUserTags("existing, canada\nlocal", result)
	want := "existing,canada,local,public-safety,Canadian Politics"
	if got != want {
		t.Fatalf("MergeUserTags() = %q, want %q", got, want)
	}
}
