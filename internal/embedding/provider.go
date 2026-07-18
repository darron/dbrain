package embedding

import (
	"context"
	"fmt"
	"strings"
)

type Purpose string

const (
	PurposeDocument Purpose = "document"
	PurposeQuery    Purpose = "query"
)

type Request struct {
	Texts   []string
	Purpose Purpose
}

type Response struct {
	Vectors    [][]float32
	Provider   string
	Model      string
	Dimensions int
}

type Info struct {
	Provider   string
	Model      string
	Dimensions int
}

type Provider interface {
	Info() Info
	Embed(context.Context, Request) (Response, error)
}

func (i Info) Validate() error {
	if strings.TrimSpace(i.Provider) == "" {
		return fmt.Errorf("embedding provider is required")
	}
	if strings.TrimSpace(i.Model) == "" {
		return fmt.Errorf("embedding model is required")
	}
	if i.Dimensions <= 0 {
		return fmt.Errorf("embedding dimensions must be positive")
	}
	return nil
}

func ValidateRequest(req Request) error {
	switch req.Purpose {
	case PurposeDocument, PurposeQuery:
	default:
		return fmt.Errorf("invalid embedding purpose %q", req.Purpose)
	}
	if len(req.Texts) == 0 {
		return fmt.Errorf("embedding request texts are required")
	}
	for i, value := range req.Texts {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("embedding request text %d is empty", i)
		}
	}
	return nil
}

func ValidateResponse(req Request, response Response) error {
	if err := ValidateRequest(req); err != nil {
		return err
	}
	if err := (Info{
		Provider: response.Provider, Model: response.Model, Dimensions: response.Dimensions,
	}).Validate(); err != nil {
		return fmt.Errorf("invalid embedding response provenance: %w", err)
	}
	if len(response.Vectors) != len(req.Texts) {
		return fmt.Errorf("embedding response vector count %d does not match request text count %d", len(response.Vectors), len(req.Texts))
	}
	for i, vector := range response.Vectors {
		if err := ValidateDenseF32(vector, response.Dimensions, NormalizationNone); err != nil {
			return fmt.Errorf("invalid embedding response vector %d: %w", i, err)
		}
	}
	return nil
}
