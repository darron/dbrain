package embeddingtest

import (
	"context"
	"fmt"
	"sync"

	"github.com/darron/dbrain/internal/embedding"
)

type Fake struct {
	info    embedding.Info
	vectors map[string][]float32

	mu    sync.Mutex
	calls []embedding.Request
}

var _ embedding.Provider = (*Fake)(nil)

func New(info embedding.Info, vectors map[string][]float32) *Fake {
	owned := make(map[string][]float32, len(vectors))
	for text, vector := range vectors {
		owned[text] = append([]float32(nil), vector...)
	}
	return &Fake{info: info, vectors: owned}
}

func (f *Fake) Info() embedding.Info {
	return f.info
}

func (f *Fake) Embed(ctx context.Context, req embedding.Request) (embedding.Response, error) {
	if err := ctx.Err(); err != nil {
		return embedding.Response{}, err
	}
	if err := f.info.Validate(); err != nil {
		return embedding.Response{}, embedding.FatalConfigError(err)
	}
	if err := embedding.ValidateRequest(req); err != nil {
		return embedding.Response{}, embedding.BlockedError(err)
	}
	f.record(req)

	response := embedding.Response{
		Provider: f.info.Provider, Model: f.info.Model, Dimensions: f.info.Dimensions,
		Vectors: make([][]float32, len(req.Texts)),
	}
	for i, text := range req.Texts {
		vector, ok := f.vectors[text]
		if !ok {
			return embedding.Response{}, embedding.BlockedError(fmt.Errorf("strict fake has no embedding for text %q", text))
		}
		response.Vectors[i] = append([]float32(nil), vector...)
	}
	if err := embedding.ValidateResponse(req, response); err != nil {
		return embedding.Response{}, embedding.FatalConfigError(fmt.Errorf("strict fake response: %w", err))
	}
	return response, nil
}

func (f *Fake) Calls() []embedding.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]embedding.Request, len(f.calls))
	for i, call := range f.calls {
		result[i] = cloneRequest(call)
	}
	return result
}

func (f *Fake) record(req embedding.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, cloneRequest(req))
}

func cloneRequest(req embedding.Request) embedding.Request {
	return embedding.Request{Purpose: req.Purpose, Texts: append([]string(nil), req.Texts...)}
}
