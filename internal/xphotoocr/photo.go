package xphotoocr

import (
	"context"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/model"
)

func ocrPhoto(ctx context.Context, absolutePath string, ref model.ItemMediaRef, opts Options) (ocrBlock, bool, bool, error) {
	heading := fmt.Sprintf("Photo %d", ref.Ordinal+1)
	hostedAttempted := false
	hostedFallback := false
	if ollamaModel, ok := parseOllamaModel(opts.Model); ok {
		text, modelName, err := ocrWithOllama(ctx, absolutePath, opts, ollamaModel)
		if err == nil && strings.TrimSpace(text) != "" {
			return ocrBlock{
				Heading:     heading,
				LocalPath:   ref.LocalPath,
				RemoteURL:   ref.RemoteURL,
				ExpandedURL: ref.ExpandedURL,
				Tool:        ollamaVisionTool,
				Model:       modelName,
				Text:        text,
			}, hostedAttempted, hostedFallback, nil
		}
	}

	if focrModel, ok := parseFrankenOCRModel(opts.Model); ok {
		text, modelName, err := ocrWithFrankenOCR(ctx, absolutePath, opts, focrModel)
		if err == nil && strings.TrimSpace(text) != "" {
			return ocrBlock{
				Heading:     heading,
				LocalPath:   ref.LocalPath,
				RemoteURL:   ref.RemoteURL,
				ExpandedURL: ref.ExpandedURL,
				Tool:        frankenOCRTool,
				Model:       modelName,
				Text:        text,
			}, hostedAttempted, hostedFallback, nil
		}
	}

	if _, ok := parseOpenRouterModel(opts.Model); ok && strings.TrimSpace(opts.OpenRouterKey) != "" {
		hostedAttempted = true
		text, modelName, err := ocrWithOpenRouter(ctx, absolutePath, opts)
		if err == nil && strings.TrimSpace(text) != "" {
			return ocrBlock{
				Heading:     heading,
				LocalPath:   ref.LocalPath,
				RemoteURL:   ref.RemoteURL,
				ExpandedURL: ref.ExpandedURL,
				Tool:        openRouterVisionTool,
				Model:       modelName,
				Text:        text,
			}, hostedAttempted, hostedFallback, nil
		}
		hostedFallback = true
	}

	text, err := ocrWithTesseract(ctx, absolutePath, opts.TesseractBinary, opts.Timeout)
	if err != nil {
		return ocrBlock{}, hostedAttempted, hostedFallback, err
	}
	return ocrBlock{
		Heading:     heading,
		LocalPath:   ref.LocalPath,
		RemoteURL:   ref.RemoteURL,
		ExpandedURL: ref.ExpandedURL,
		Tool:        tesseractTool,
		Model:       "",
		Text:        text,
	}, hostedAttempted, hostedFallback, nil
}

func ocrPhotoWithModel(ctx context.Context, absolutePath string, ref model.ItemMediaRef, opts Options) (ocrBlock, error) {
	heading := fmt.Sprintf("Photo %d", ref.Ordinal+1)
	if strings.EqualFold(strings.TrimSpace(opts.Model), "tesseract") {
		text, err := ocrWithTesseract(ctx, absolutePath, opts.TesseractBinary, opts.Timeout)
		if err != nil {
			return ocrBlock{}, err
		}
		return ocrBlock{
			Heading:     heading,
			LocalPath:   ref.LocalPath,
			RemoteURL:   ref.RemoteURL,
			ExpandedURL: ref.ExpandedURL,
			Tool:        tesseractTool,
			Model:       "tesseract",
			Text:        text,
		}, nil
	}
	if ollamaModel, ok := parseOllamaModel(opts.Model); ok {
		text, modelName, err := ocrWithOllama(ctx, absolutePath, opts, ollamaModel)
		if err != nil {
			return ocrBlock{}, err
		}
		return ocrBlock{
			Heading:     heading,
			LocalPath:   ref.LocalPath,
			RemoteURL:   ref.RemoteURL,
			ExpandedURL: ref.ExpandedURL,
			Tool:        ollamaVisionTool,
			Model:       modelName,
			Text:        text,
		}, nil
	}
	if focrModel, ok := parseFrankenOCRModel(opts.Model); ok {
		text, modelName, err := ocrWithFrankenOCR(ctx, absolutePath, opts, focrModel)
		if err != nil {
			return ocrBlock{}, err
		}
		return ocrBlock{
			Heading:     heading,
			LocalPath:   ref.LocalPath,
			RemoteURL:   ref.RemoteURL,
			ExpandedURL: ref.ExpandedURL,
			Tool:        frankenOCRTool,
			Model:       modelName,
			Text:        text,
		}, nil
	}
	if _, ok := parseOpenRouterModel(opts.Model); ok {
		if strings.TrimSpace(opts.OpenRouterKey) == "" {
			return ocrBlock{}, fmt.Errorf("openrouter OCR key is required for model %s", opts.Model)
		}
		text, modelName, err := ocrWithOpenRouter(ctx, absolutePath, opts)
		if err != nil {
			return ocrBlock{}, err
		}
		return ocrBlock{
			Heading:     heading,
			LocalPath:   ref.LocalPath,
			RemoteURL:   ref.RemoteURL,
			ExpandedURL: ref.ExpandedURL,
			Tool:        openRouterVisionTool,
			Model:       modelName,
			Text:        text,
		}, nil
	}
	return ocrBlock{}, fmt.Errorf("unsupported OCR model %q; use ollama/<name>, openrouter/<provider>/<model>, focr[/default], franken_ocr[/default], or tesseract", opts.Model)
}

func renderOCRBlocks(blocks []ocrBlock) string {
	var b strings.Builder
	for i, block := range blocks {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(block.Heading)
		b.WriteString("\n")
		b.WriteString(block.Text)
	}
	return b.String()
}
