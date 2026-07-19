package embedding

import (
	"errors"
	"testing"
)

func TestValidateRequestRequiresOrderedNonEmptyTextBatch(t *testing.T) {
	t.Parallel()
	for _, req := range []Request{
		{},
		{Purpose: PurposeDocument},
		{Purpose: PurposeDocument, Texts: []string{"alpha", " "}},
		{Purpose: Purpose("other"), Texts: []string{"alpha"}},
	} {
		if err := ValidateRequest(req); err == nil {
			t.Fatalf("ValidateRequest(%+v) unexpectedly succeeded", req)
		}
	}
	if err := ValidateRequest(Request{Purpose: PurposeQuery, Texts: []string{"alpha", "bravo"}}); err != nil {
		t.Fatalf("valid request: %v", err)
	}
}

func TestValidateResponseRequiresExactCardinalityAndFixedDimensions(t *testing.T) {
	t.Parallel()
	req := Request{Purpose: PurposeDocument, Texts: []string{"alpha", "bravo"}}
	valid := Response{
		Provider: "fake", Model: "fake-v1", Dimensions: 2,
		Vectors: [][]float32{{1, 0}, {0, 1}},
	}
	expected := Info{Provider: "fake", Model: "fake-v1", Dimensions: 2}
	if err := ValidateResponse(expected, req, valid); err != nil {
		t.Fatalf("valid response: %v", err)
	}

	wrongCount := valid
	wrongCount.Vectors = wrongCount.Vectors[:1]
	if err := ValidateResponse(expected, req, wrongCount); err == nil {
		t.Fatal("response with wrong vector count unexpectedly succeeded")
	}
	wrongDimensions := valid
	wrongDimensions.Vectors = [][]float32{{1, 0}, {1}}
	if err := ValidateResponse(expected, req, wrongDimensions); err == nil {
		t.Fatal("response with mixed dimensions unexpectedly succeeded")
	}
	invalidInfo := valid
	invalidInfo.Dimensions = 0
	if err := ValidateResponse(expected, req, invalidInfo); err == nil {
		t.Fatal("response with non-positive dimensions unexpectedly succeeded")
	}
}

func TestValidateResponseRequiresExpectedProvenance(t *testing.T) {
	t.Parallel()
	req := Request{Purpose: PurposeQuery, Texts: []string{"alpha"}}
	expected := Info{Provider: "ollama", Model: "nomic-embed-text:latest", Dimensions: 2}
	valid := Response{
		Provider: expected.Provider, Model: expected.Model, Dimensions: expected.Dimensions,
		Vectors: [][]float32{{1, 0}},
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Response)
	}{
		{name: "provider", mutate: func(response *Response) { response.Provider = "hosted" }},
		{name: "exact model", mutate: func(response *Response) { response.Model = "nomic-embed-text:v2" }},
		{name: "dimensions", mutate: func(response *Response) { response.Dimensions = 3; response.Vectors = [][]float32{{1, 0, 0}} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := valid
			tc.mutate(&response)
			if err := ValidateResponse(expected, req, response); err == nil {
				t.Fatalf("mismatched %s provenance unexpectedly succeeded", tc.name)
			}
		})
	}
}

func TestInfoValidationRequiresExactProviderModelAndDimensions(t *testing.T) {
	t.Parallel()
	if err := (Info{Provider: "ollama", Model: "nomic-embed-text:latest", Dimensions: 768}).Validate(); err != nil {
		t.Fatalf("valid info: %v", err)
	}
	for _, info := range []Info{
		{Model: "model", Dimensions: 2},
		{Provider: "provider", Dimensions: 2},
		{Provider: "provider", Model: "model"},
	} {
		if err := info.Validate(); err == nil {
			t.Fatalf("Info.Validate(%+v) unexpectedly succeeded", info)
		}
	}
}

func TestTypedProviderFailuresPreserveClassificationAndCause(t *testing.T) {
	t.Parallel()
	cause := errors.New("provider unavailable")
	for _, tc := range []struct {
		name string
		err  error
		is   func(error) bool
	}{
		{name: "retryable", err: RetryableError(cause), is: IsRetryable},
		{name: "blocked", err: BlockedError(cause), is: IsBlocked},
		{name: "fatal config", err: FatalConfigError(cause), is: IsFatalConfig},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.is(tc.err) || !errors.Is(tc.err, cause) {
				t.Fatalf("classified error = %v, is=%v cause=%v", tc.err, tc.is(tc.err), errors.Is(tc.err, cause))
			}
		})
	}
	if IsRetryable(BlockedError(cause)) || IsBlocked(FatalConfigError(cause)) || IsFatalConfig(RetryableError(cause)) {
		t.Fatal("provider failure classifications overlap")
	}
}
