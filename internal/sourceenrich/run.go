package sourceenrich

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"sort"
	"strings"
	"time"

	"dbrain/internal/config"
	"dbrain/internal/model"
	"dbrain/internal/store"
	"dbrain/internal/summarizecli"
	"dbrain/internal/vault"
)

const SummaryPromptVersion = "dbrain-v1"

const summaryPrompt = `Summarize this source for a local second-brain knowledge base.
Focus on durable knowledge, concrete facts, named entities, tools, libraries, APIs, claims, and actionable takeaways.
Use Markdown with exactly these headings:
### What It Is
### Key Ideas
### Why It Matters
### Entities
### Follow-ups
Keep it factual and concise.
Use bullets only in Entities and Follow-ups.
Do not mention ads, sponsors, or irrelevant boilerplate.`

type Options struct {
	Limit     int
	Force     bool
	Summarize bool
	Model     string
	CLI       string
	Length    string
	Timeout   time.Duration
	Logger    *slog.Logger
	EnvFor    func(source model.SourceDocument) map[string]string
	ArgsFor   func(source model.SourceDocument) []string
	Binary    string
}

type Stats struct {
	SourcesQueued     int `json:"sources_queued"`
	SourcesExtracted  int `json:"sources_extracted"`
	SourcesSummarized int `json:"sources_summarized"`
	SourcesRendered   int `json:"sources_rendered"`
	SourcesUnchanged  int `json:"sources_unchanged"`
	Errors            int `json:"errors"`
}

func RunPending(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, []int64, error) {
	toolVersion := summarizecli.Version(ctx, opts.Binary)
	sources, err := st.ListSourcesForEnrichment(ctx, opts.Limit, opts.Force, opts.Summarize, SummaryPromptVersion, summarizecli.ToolName, toolVersion)
	if err != nil {
		return Stats{}, nil, err
	}
	return runSources(ctx, cfg, st, sources, opts, toolVersion)
}

func RunSourceIDs(ctx context.Context, cfg config.Config, st *store.Store, sourceIDs []int64, opts Options) (Stats, []int64, error) {
	ordered := uniqueSorted(sourceIDs)
	sources, err := st.GetSourcesByIDs(ctx, ordered)
	if err != nil {
		return Stats{}, nil, err
	}

	byID := make(map[int64]model.SourceDocument, len(sources))
	for _, source := range sources {
		byID[source.ID] = source
	}

	filtered := make([]model.SourceDocument, 0, len(ordered))
	for _, sourceID := range ordered {
		source, ok := byID[sourceID]
		if !ok {
			continue
		}
		if !opts.Force && !needsEnrichment(source, opts, summarizecli.ToolName, summarizecli.Version(ctx, opts.Binary)) {
			continue
		}
		filtered = append(filtered, source)
	}

	return runSources(ctx, cfg, st, filtered, opts, summarizecli.Version(ctx, opts.Binary))
}

func runSources(ctx context.Context, cfg config.Config, st *store.Store, sources []model.SourceDocument, opts Options, toolVersion string) (Stats, []int64, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Minute
	}
	if opts.Length == "" {
		opts.Length = "medium"
	}

	stats := Stats{SourcesQueued: len(sources)}
	touchedSourceIDs := map[int64]struct{}{}

	debugLog(opts.Logger, "source enrichment candidates loaded", "sources", len(sources), "limit", opts.Limit, "summarize", opts.Summarize)

	for _, source := range sources {
		debugLog(opts.Logger, "enriching source", "source_key", source.SourceKey, "url", source.CanonicalURL)
		sourceArgs := argsFor(opts, source)
		sourceEnv := envFor(opts, source)
		localExtract, hasLocalExtract, err := st.GetPreferredLocalSourceExtract(ctx, source.ID)
		if err != nil {
			return stats, nil, err
		}
		if hasLocalExtract {
			debugLog(opts.Logger, "using local cached extract", "source_key", source.SourceKey, "url", source.CanonicalURL, "content_chars", len(localExtract.Content))
			contentHash := hashText(localExtract.Content)
			if changed, err := st.SaveSourceExtraction(ctx, source.ID, localExtract, contentHash); err != nil {
				return stats, nil, err
			} else if changed {
				stats.SourcesExtracted++
			} else {
				stats.SourcesUnchanged++
			}

			if opts.Summarize {
				runResult, err := summarizecli.Run(ctx, summarizecli.Options{
					Binary:    opts.Binary,
					Input:     "-",
					Stdin:     summaryInput(localExtract),
					Summarize: true,
					Model:     opts.Model,
					CLI:       opts.CLI,
					Prompt:    buildSummaryPrompt(source, localExtract),
					Length:    opts.Length,
					Timeout:   opts.Timeout,
					Env:       sourceEnv,
				})
				if err != nil {
					stats.Errors++
					debugLog(opts.Logger, "local source summarization failed", "source_key", source.SourceKey, "url", source.CanonicalURL, "error", err.Error())
					if _, saveErr := st.SaveSourceSummary(ctx, source.ID, model.SummaryResult{
						Status:        "error",
						Error:         err.Error(),
						Model:         opts.Model,
						PromptVersion: SummaryPromptVersion,
						Tool:          summarizecli.ToolName,
						ToolVersion:   toolVersion,
					}); saveErr != nil {
						return stats, nil, saveErr
					}
					touchedSourceIDs[source.ID] = struct{}{}
					continue
				}
				runResult.Summary.PromptVersion = SummaryPromptVersion
				if changed, err := st.SaveSourceSummary(ctx, source.ID, runResult.Summary); err != nil {
					return stats, nil, err
				} else if changed && runResult.Summary.Status == "ok" {
					stats.SourcesSummarized++
				}
			}

			touchedSourceIDs[source.ID] = struct{}{}
			continue
		}

		if opts.Summarize && len(sourceArgs) > 0 {
			extractResult, err := summarizecli.Run(ctx, summarizecli.Options{
				Binary:    opts.Binary,
				Input:     source.CanonicalURL,
				Summarize: false,
				Length:    opts.Length,
				Timeout:   opts.Timeout,
				Env:       sourceEnv,
				Args:      sourceArgs,
			})
			if err != nil {
				stats.Errors++
				debugLog(opts.Logger, "source extraction failed", "source_key", source.SourceKey, "url", source.CanonicalURL, "error", err.Error())
				if _, saveErr := st.SaveSourceExtraction(ctx, source.ID, model.ExtractResult{
					Status:      "error",
					Error:       err.Error(),
					Tool:        summarizecli.ToolName,
					ToolVersion: toolVersion,
				}, source.ContentHash); saveErr != nil {
					return stats, nil, saveErr
				}
				if _, saveErr := st.SaveSourceSummary(ctx, source.ID, model.SummaryResult{
					Status:        "error",
					Error:         err.Error(),
					Model:         opts.Model,
					PromptVersion: SummaryPromptVersion,
					Tool:          summarizecli.ToolName,
					ToolVersion:   toolVersion,
				}); saveErr != nil {
					return stats, nil, saveErr
				}
				touchedSourceIDs[source.ID] = struct{}{}
				continue
			}

			contentHash := hashText(extractResult.Extract.Content)
			if changed, err := st.SaveSourceExtraction(ctx, source.ID, extractResult.Extract, contentHash); err != nil {
				return stats, nil, err
			} else if changed {
				stats.SourcesExtracted++
			} else {
				stats.SourcesUnchanged++
			}

			if changed, err := summarizeFromExtract(ctx, st, source, extractResult.Extract, opts, toolVersion); err != nil {
				return stats, nil, err
			} else if changed {
				stats.SourcesSummarized++
			}

			touchedSourceIDs[source.ID] = struct{}{}
			continue
		}

		runResult, err := summarizecli.Run(ctx, summarizecli.Options{
			Binary:    opts.Binary,
			Input:     source.CanonicalURL,
			Summarize: opts.Summarize,
			Model:     opts.Model,
			CLI:       opts.CLI,
			Prompt:    summaryPrompt,
			Length:    opts.Length,
			Timeout:   opts.Timeout,
			Env:       sourceEnv,
			Args:      sourceArgs,
		})
		if err != nil {
			stats.Errors++
			debugLog(opts.Logger, "source enrichment failed", "source_key", source.SourceKey, "url", source.CanonicalURL, "error", err.Error())
			if _, saveErr := st.SaveSourceExtraction(ctx, source.ID, model.ExtractResult{
				Status:      "error",
				Error:       err.Error(),
				Tool:        summarizecli.ToolName,
				ToolVersion: toolVersion,
			}, source.ContentHash); saveErr != nil {
				return stats, nil, saveErr
			}
			if opts.Summarize {
				if _, saveErr := st.SaveSourceSummary(ctx, source.ID, model.SummaryResult{
					Status:        "error",
					Error:         err.Error(),
					Model:         opts.Model,
					PromptVersion: SummaryPromptVersion,
					Tool:          summarizecli.ToolName,
					ToolVersion:   toolVersion,
				}); saveErr != nil {
					return stats, nil, saveErr
				}
			}
			touchedSourceIDs[source.ID] = struct{}{}
			continue
		}

		contentHash := hashText(runResult.Extract.Content)
		if changed, err := st.SaveSourceExtraction(ctx, source.ID, runResult.Extract, contentHash); err != nil {
			return stats, nil, err
		} else if changed {
			stats.SourcesExtracted++
		} else {
			stats.SourcesUnchanged++
		}

		if opts.Summarize {
			runResult.Summary.PromptVersion = SummaryPromptVersion
			if changed, err := st.SaveSourceSummary(ctx, source.ID, runResult.Summary); err != nil {
				return stats, nil, err
			} else if changed && runResult.Summary.Status == "ok" {
				stats.SourcesSummarized++
			}
		}

		touchedSourceIDs[source.ID] = struct{}{}
	}

	orderedSourceIDs := uniqueSorted(mapKeys(touchedSourceIDs))
	for _, sourceID := range orderedSourceIDs {
		source, err := st.GetSourceByID(ctx, sourceID)
		if err != nil {
			return stats, nil, err
		}
		backlinks, err := st.ListBacklinksForSource(ctx, sourceID)
		if err != nil {
			return stats, nil, err
		}
		if err := vault.WriteSource(cfg, source, backlinks); err != nil {
			return stats, nil, err
		}
		stats.SourcesRendered++
	}

	return stats, orderedSourceIDs, nil
}

func needsEnrichment(source model.SourceDocument, opts Options, toolName string, toolVersion string) bool {
	if source.ExtractStatus == "" || source.ExtractStatus == "error" {
		return true
	}
	if !opts.Summarize {
		return false
	}
	if source.ExtractStatus != "ok" && source.ExtractStatus != "empty" {
		return false
	}
	if source.SummaryStatus == "" || source.SummaryStatus == "error" {
		return true
	}
	if source.SummaryContentHash != source.ContentHash {
		return true
	}
	if source.SummaryPromptVersion != SummaryPromptVersion {
		return true
	}
	if toolName != "" && source.SummaryTool != toolName {
		return true
	}
	if toolVersion != "" && source.SummaryToolVersion != toolVersion {
		return true
	}
	return false
}

func envFor(opts Options, source model.SourceDocument) map[string]string {
	if opts.EnvFor == nil {
		return nil
	}
	return opts.EnvFor(source)
}

func argsFor(opts Options, source model.SourceDocument) []string {
	if opts.ArgsFor == nil {
		return nil
	}
	return opts.ArgsFor(source)
}

func buildSummaryPrompt(source model.SourceDocument, extract model.ExtractResult) string {
	var b strings.Builder
	b.WriteString(summaryPrompt)

	contextLines := make([]string, 0, 3)
	if value := strings.TrimSpace(source.CanonicalURL); value != "" {
		contextLines = append(contextLines, "Source URL: "+value)
	}
	title := strings.TrimSpace(extract.Title)
	if title == "" {
		title = strings.TrimSpace(source.Title)
	}
	if title != "" {
		contextLines = append(contextLines, "Source Title: "+title)
	}
	site := strings.TrimSpace(extract.SiteName)
	if site == "" {
		site = strings.TrimSpace(source.SiteName)
	}
	if site == "" {
		site = strings.TrimSpace(source.Domain)
	}
	if site != "" {
		contextLines = append(contextLines, "Source Site: "+site)
	}

	if len(contextLines) == 0 {
		return b.String()
	}

	b.WriteString("\n\nAdditional context:\n")
	for _, line := range contextLines {
		b.WriteString(line)
		b.WriteByte('\n')
	}

	return strings.TrimSpace(b.String())
}

func summarizeFromExtract(ctx context.Context, st *store.Store, source model.SourceDocument, extract model.ExtractResult, opts Options, toolVersion string) (bool, error) {
	if reason, ok := skipSummaryReason(source, extract); ok {
		return st.SaveSourceSummary(ctx, source.ID, model.SummaryResult{
			Status:        "skipped",
			Error:         reason,
			Model:         opts.Model,
			PromptVersion: SummaryPromptVersion,
			Tool:          summarizecli.ToolName,
			ToolVersion:   toolVersion,
			FetchedAt:     time.Now().UTC(),
		})
	}

	input := summaryInput(extract)
	if strings.TrimSpace(input) == "" {
		return st.SaveSourceSummary(ctx, source.ID, model.SummaryResult{
			Status:        "error",
			Error:         "no extracted content available for summary",
			Model:         opts.Model,
			PromptVersion: SummaryPromptVersion,
			Tool:          summarizecli.ToolName,
			ToolVersion:   toolVersion,
		})
	}

	runResult, err := summarizecli.Run(ctx, summarizecli.Options{
		Binary:    opts.Binary,
		Input:     "-",
		Stdin:     input,
		Summarize: true,
		Model:     opts.Model,
		CLI:       opts.CLI,
		Prompt:    buildSummaryPrompt(source, extract),
		Length:    opts.Length,
		Timeout:   opts.Timeout,
	})
	if err != nil {
		return st.SaveSourceSummary(ctx, source.ID, model.SummaryResult{
			Status:        "error",
			Error:         err.Error(),
			Model:         opts.Model,
			PromptVersion: SummaryPromptVersion,
			Tool:          summarizecli.ToolName,
			ToolVersion:   toolVersion,
		})
	}

	runResult.Summary.PromptVersion = SummaryPromptVersion
	return st.SaveSourceSummary(ctx, source.ID, runResult.Summary)
}

func summaryInput(extract model.ExtractResult) string {
	parts := make([]string, 0, 4)
	if title := strings.TrimSpace(extract.Title); title != "" {
		parts = append(parts, "Title: "+title)
	}
	if description := strings.TrimSpace(extract.Description); description != "" {
		parts = append(parts, "Description: "+description)
	}
	if siteName := strings.TrimSpace(extract.SiteName); siteName != "" {
		parts = append(parts, "Site: "+siteName)
	}
	if content := strings.TrimSpace(extract.Content); content != "" {
		parts = append(parts, content)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

type youtubeExtractEnvelope struct {
	Extracted struct {
		TranscriptSource      *string `json:"transcriptSource"`
		TranscriptionProvider *string `json:"transcriptionProvider"`
		TranscriptCharacters  *int    `json:"transcriptCharacters"`
	} `json:"extracted"`
}

func skipSummaryReason(source model.SourceDocument, extract model.ExtractResult) (string, bool) {
	if source.SourceType != "youtube" {
		return "", false
	}
	if strings.TrimSpace(extract.RawJSON) == "" {
		return "", false
	}

	var payload youtubeExtractEnvelope
	if err := json.Unmarshal([]byte(extract.RawJSON), &payload); err != nil {
		return "", false
	}

	transcriptSource := ""
	if payload.Extracted.TranscriptSource != nil {
		transcriptSource = strings.TrimSpace(*payload.Extracted.TranscriptSource)
	}
	transcriptionProvider := ""
	if payload.Extracted.TranscriptionProvider != nil {
		transcriptionProvider = strings.TrimSpace(*payload.Extracted.TranscriptionProvider)
	}
	transcriptChars := 0
	if payload.Extracted.TranscriptCharacters != nil {
		transcriptChars = *payload.Extracted.TranscriptCharacters
	}

	if transcriptChars > 0 || transcriptionProvider != "" {
		return "", false
	}
	if transcriptSource == "captionTracks" || transcriptSource == "youtubei" {
		return "", false
	}
	if transcriptSource == "unavailable" && len(strings.TrimSpace(extract.Content)) <= 200 {
		return "youtube transcript unavailable and no audio transcription was produced", true
	}
	return "", false
}

func hashText(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}

func uniqueSorted(values []int64) []int64 {
	seen := map[int64]struct{}{}
	out := make([]int64, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func mapKeys(values map[int64]struct{}) []int64 {
	out := make([]int64, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	return out
}

func debugLog(logger *slog.Logger, msg string, args ...any) {
	if logger == nil {
		return
	}
	logger.Debug(msg, args...)
}
