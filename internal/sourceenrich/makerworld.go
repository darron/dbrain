package sourceenrich

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
)

const (
	makerWorldExtractToolName    = "makerworld-api"
	makerWorldExtractToolVersion = "makerworld-api-v1"
)

var makerWorldModelPathPattern = regexp.MustCompile(`(?i)(?:^|/)models/(\d+)`)
var makerWorldAPIBaseURL = "https://api.bambulab.com/v1/design-service/design/"

type makerWorldDesign struct {
	ID                         int64                   `json:"id"`
	Title                      string                  `json:"title"`
	TitleTranslated            string                  `json:"titleTranslated"`
	Slug                       string                  `json:"slug"`
	CoverURL                   string                  `json:"coverUrl"`
	Summary                    string                  `json:"summary"`
	SummaryTranslated          string                  `json:"summaryTranslated"`
	LikeCount                  int                     `json:"likeCount"`
	CollectionCount            int                     `json:"collectionCount"`
	PrintCount                 int                     `json:"printCount"`
	CommentCount               int                     `json:"commentCount"`
	DownloadCount              int                     `json:"downloadCount"`
	RawModelFileDownloadCount  int                     `json:"rawModelFileDownloadCount"`
	Tags                       []string                `json:"tags"`
	TagsTranslated             []string                `json:"tagsTranslated"`
	Categories                 []makerWorldNamedValue  `json:"categories"`
	DesignCreator              makerWorldCreator       `json:"designCreator"`
	Instances                  []makerWorldInstance    `json:"instances"`
	License                    string                  `json:"license"`
	LicenseDescriptionInfo     makerWorldLicenseInfo   `json:"licenseDescriptionInfo"`
	AllowReCreation            bool                    `json:"allowReCreation"`
	NSFW                       bool                    `json:"nsfw"`
	CreateTime                 string                  `json:"createTime"`
	UpdateTime                 string                  `json:"updateTime"`
	ModelID                    string                  `json:"modelId"`
	DefaultInstanceID          int64                   `json:"defaultInstanceId"`
	IsPrintable                bool                    `json:"isPrintable"`
	IsOfficial                 bool                    `json:"isOfficial"`
	IsStaffPicked              bool                    `json:"isStaffPicked"`
	DesignExtension            makerWorldDesignExt     `json:"designExtension"`
	DesignCreatorCustomization makerWorldCustomization `json:"designCreatorCustomization"`
}

type makerWorldNamedValue struct {
	Name string `json:"name"`
}

type makerWorldCreator struct {
	UID    int64  `json:"uid"`
	Name   string `json:"name"`
	Handle string `json:"handle"`
	Level  int    `json:"level"`
}

type makerWorldInstance struct {
	ID                int64                `json:"id"`
	ProfileID         int64                `json:"profileId"`
	Title             string               `json:"title"`
	TitleTranslated   string               `json:"titleTranslated"`
	Summary           string               `json:"summary"`
	SummaryTranslated string               `json:"summaryTranslated"`
	CreateTime        string               `json:"createTime"`
	UpdateTime        string               `json:"updateTime"`
	InstanceCreator   makerWorldCreator    `json:"instanceCreator"`
	DownloadCount     int                  `json:"downloadCount"`
	PrintCount        int                  `json:"printCount"`
	RatingCount       int                  `json:"ratingCount"`
	Score             float64              `json:"score"`
	Prediction        int                  `json:"prediction"`
	Weight            int                  `json:"weight"`
	NeedAMS           bool                 `json:"needAms"`
	HasZipSTL         bool                 `json:"hasZipStl"`
	InstanceFilaments []makerWorldFilament `json:"instanceFilaments"`
	Pictures          []makerWorldPicture  `json:"pictures"`
}

type makerWorldFilament struct {
	Type  string `json:"type"`
	Color string `json:"color"`
	UsedG string `json:"usedG"`
}

type makerWorldPicture struct {
	Name            string `json:"name"`
	URL             string `json:"url"`
	IsRealLifePhoto int    `json:"isRealLifePhoto"`
}

type makerWorldDesignExt struct {
	DesignPictures []makerWorldPicture   `json:"design_pictures"`
	ModelFiles     []makerWorldModelFile `json:"model_files"`
}

type makerWorldModelFile struct {
	ModelName string `json:"modelName"`
	ModelSize int64  `json:"modelSize"`
	ModelType string `json:"modelType"`
	Note      string `json:"note"`
}

type makerWorldLicenseInfo struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

type makerWorldCustomization struct {
	DesignHeader           string `json:"designHeader"`
	DesignHeaderTranslated string `json:"designHeaderTranslated"`
	DesignFooter           string `json:"designFooter"`
	DesignFooterTranslated string `json:"designFooterTranslated"`
}

func processMakerWorldAPIExtract(processCtx sourceProcessContext) (sourceProcessResult, bool) {
	var result sourceProcessResult
	ctx := processCtx.ctx
	cfg := processCtx.cfg
	st := processCtx.st
	source := processCtx.source
	opts := processCtx.opts

	extract, handled, err := extractMakerWorldAPISource(ctx, source, opts)
	if !handled {
		return result, false
	}
	if err != nil {
		result.Stats.Errors++
		debugLog(opts.Logger, "makerworld API extraction failed", "source_key", source.SourceKey, "url", source.CanonicalURL, "error", err.Error())
		failure := model.ExtractResult{
			Status:      model.SourceExtractStatusError,
			Error:       err.Error(),
			Tool:        makerWorldExtractToolName,
			ToolVersion: makerWorldExtractToolVersion,
		}
		if err := saveSourceFailure(ctx, st, source, failure, opts, processCtx.extractToolVersion, processCtx.summaryToolVersion); err != nil {
			result.Err = err
			return result, true
		}
		result.TouchedSourceID = source.ID
		return result, true
	}

	debugLog(opts.Logger, "using makerworld API extract", "source_key", source.SourceKey, "url", source.CanonicalURL, "content_chars", len(extract.Content), "tool", extract.Tool)
	stats, err := persistExtractAndSummaryFromExtract(ctx, cfg, st, source, extract, opts, processCtx.extractToolVersion, processCtx.summaryToolVersion)
	if err != nil {
		result.Err = err
		return result, true
	}
	result.Stats.SourcesExtracted += stats.SourcesExtracted
	result.Stats.SourcesSummarized += stats.SourcesSummarized
	result.Stats.SourcesUnchanged += stats.SourcesUnchanged
	result.Stats.Errors += stats.Errors
	result.TouchedSourceID = source.ID
	return result, true
}

func extractMakerWorldAPISource(ctx context.Context, source model.SourceDocument, opts Options) (model.ExtractResult, bool, error) {
	sourceURL := firstNonEmpty(source.CanonicalURL, source.NormalizedURL)
	modelID, ok := makerWorldModelID(sourceURL)
	if !ok {
		return model.ExtractResult{}, false, nil
	}

	timeout := 30 * time.Second
	if opts.Timeout > 0 && opts.Timeout < timeout {
		timeout = opts.Timeout
	}
	fetchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	apiURL := strings.TrimRight(makerWorldAPIBaseURL, "/") + "/" + modelID
	body, err := fetchMakerWorldAPIText(fetchCtx, apiURL)
	if err != nil {
		return model.ExtractResult{}, true, err
	}

	var design makerWorldDesign
	if err := json.Unmarshal([]byte(body), &design); err != nil {
		return model.ExtractResult{}, true, fmt.Errorf("parse makerworld design json: %w", err)
	}
	if design.ID == 0 {
		return model.ExtractResult{}, true, fmt.Errorf("makerworld API returned no design id for model %s", modelID)
	}

	content := makerWorldExtractContent(sourceURL, design)
	if strings.TrimSpace(content) == "" {
		return model.ExtractResult{}, true, fmt.Errorf("makerworld API returned no extractable content for model %s", modelID)
	}

	title := firstNonEmpty(strings.TrimSpace(design.Title), strings.TrimSpace(design.TitleTranslated))
	description := truncateUTF8(firstNonEmpty(
		cleanMakerWorldHTML(design.Summary),
		cleanMakerWorldHTML(design.SummaryTranslated),
	), 500)

	return model.ExtractResult{
		CanonicalURL: sourceURL,
		FinalURL:     sourceURL,
		Title:        title,
		Description:  description,
		SiteName:     "MakerWorld",
		Content:      content,
		RawJSON:      strings.TrimSpace(body),
		Status:       extractStatusForContent(content),
		FetchedAt:    time.Now().UTC(),
		Tool:         makerWorldExtractToolName,
		ToolVersion:  makerWorldExtractToolVersion,
	}, true, nil
}

func makerWorldModelID(rawURL string) (string, bool) {
	parsed, err := neturl.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", false
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	host = strings.TrimPrefix(host, "www.")
	if host != "makerworld.com" {
		return "", false
	}
	match := makerWorldModelPathPattern.FindStringSubmatch(parsed.EscapedPath())
	if len(match) != 2 {
		return "", false
	}
	if _, err := strconv.ParseInt(match[1], 10, 64); err != nil {
		return "", false
	}
	return match[1], true
}

func fetchMakerWorldAPIText(ctx context.Context, apiURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("create makerworld API request: %w", err)
	}
	req.Header.Set("user-agent", protectedFetchUserAgent)
	req.Header.Set("accept", "application/json")
	req.Header.Set("accept-language", "en-US,en;q=0.9")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch makerworld API: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProtectedBodyBytes))
	if err != nil {
		return "", fmt.Errorf("read makerworld API response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("makerworld API returned status %d", resp.StatusCode)
	}
	return string(body), nil
}

func makerWorldExtractContent(sourceURL string, design makerWorldDesign) string {
	var b strings.Builder
	title := firstNonEmpty(design.Title, design.TitleTranslated, design.Slug)
	writeMakerWorldLine(&b, "Title", title)
	writeMakerWorldLine(&b, "URL", sourceURL)
	writeMakerWorldLine(&b, "MakerWorld ID", fmt.Sprintf("%d", design.ID))
	writeMakerWorldLine(&b, "Model ID", design.ModelID)
	writeMakerWorldLine(&b, "Creator", makerWorldCreatorText(design.DesignCreator))
	writeMakerWorldLine(&b, "Categories", strings.Join(makerWorldNames(design.Categories), ", "))
	writeMakerWorldLine(&b, "Tags", strings.Join(firstNonEmptyStringSlice(design.Tags, design.TagsTranslated), ", "))
	writeMakerWorldLine(&b, "License", firstNonEmpty(design.License, design.LicenseDescriptionInfo.Title))
	writeMakerWorldLine(&b, "Created", design.CreateTime)
	writeMakerWorldLine(&b, "Updated", design.UpdateTime)
	writeMakerWorldLine(&b, "Cover", design.CoverURL)

	flags := make([]string, 0, 4)
	if design.IsOfficial {
		flags = append(flags, "official")
	}
	if design.IsStaffPicked {
		flags = append(flags, "staff-picked")
	}
	if design.IsPrintable {
		flags = append(flags, "printable")
	}
	if design.AllowReCreation {
		flags = append(flags, "allows re-creation")
	}
	if len(flags) > 0 {
		writeMakerWorldLine(&b, "Flags", strings.Join(flags, ", "))
	}

	if design.LikeCount > 0 || design.CollectionCount > 0 || design.DownloadCount > 0 || design.PrintCount > 0 || design.CommentCount > 0 {
		writeMakerWorldLine(&b, "Stats", fmt.Sprintf("likes=%d collections=%d downloads=%d raw_file_downloads=%d prints=%d comments=%d",
			design.LikeCount,
			design.CollectionCount,
			design.DownloadCount,
			design.RawModelFileDownloadCount,
			design.PrintCount,
			design.CommentCount,
		))
	}

	writeMakerWorldSection(&b, "Summary", firstNonEmpty(cleanMakerWorldHTML(design.Summary), cleanMakerWorldHTML(design.SummaryTranslated)))
	writeMakerWorldSection(&b, "Creator Notes", firstNonEmpty(
		cleanMakerWorldHTML(design.DesignCreatorCustomization.DesignHeader),
		cleanMakerWorldHTML(design.DesignCreatorCustomization.DesignHeaderTranslated),
		cleanMakerWorldHTML(design.DesignCreatorCustomization.DesignFooter),
		cleanMakerWorldHTML(design.DesignCreatorCustomization.DesignFooterTranslated),
	))
	writeMakerWorldSection(&b, "License Details", cleanMakerWorldHTML(design.LicenseDescriptionInfo.Content))

	if len(design.Instances) > 0 {
		b.WriteString("\n## Print Profiles\n\n")
		for i, instance := range design.Instances {
			if i >= 5 {
				break
			}
			b.WriteString("- ")
			b.WriteString(firstNonEmpty(instance.Title, instance.TitleTranslated, fmt.Sprintf("Profile %d", instance.ID)))
			parts := []string{}
			if creator := makerWorldCreatorText(instance.InstanceCreator); creator != "" {
				parts = append(parts, "creator: "+creator)
			}
			if instance.PrintCount > 0 {
				parts = append(parts, fmt.Sprintf("prints: %d", instance.PrintCount))
			}
			if instance.DownloadCount > 0 {
				parts = append(parts, fmt.Sprintf("downloads: %d", instance.DownloadCount))
			}
			if instance.Weight > 0 {
				parts = append(parts, fmt.Sprintf("weight_g: %d", instance.Weight))
			}
			if instance.Prediction > 0 {
				parts = append(parts, fmt.Sprintf("estimated_seconds: %d", instance.Prediction))
			}
			if instance.NeedAMS {
				parts = append(parts, "needs AMS")
			}
			if instance.HasZipSTL {
				parts = append(parts, "has STL zip")
			}
			if len(parts) > 0 {
				b.WriteString(" (")
				b.WriteString(strings.Join(parts, "; "))
				b.WriteString(")")
			}
			if summary := firstNonEmpty(cleanMakerWorldHTML(instance.Summary), cleanMakerWorldHTML(instance.SummaryTranslated)); summary != "" {
				b.WriteString(": ")
				b.WriteString(truncateUTF8(summary, 700))
			}
			if filaments := makerWorldFilamentText(instance.InstanceFilaments); filaments != "" {
				b.WriteString(" Filaments: ")
				b.WriteString(filaments)
			}
			b.WriteString("\n")
		}
	}

	if len(design.DesignExtension.ModelFiles) > 0 {
		b.WriteString("\n## Model Files\n\n")
		for i, file := range design.DesignExtension.ModelFiles {
			if i >= 8 {
				break
			}
			name := firstNonEmpty(file.ModelName, file.ModelType)
			if name == "" {
				continue
			}
			b.WriteString("- ")
			b.WriteString(name)
			parts := []string{}
			if file.ModelType != "" {
				parts = append(parts, file.ModelType)
			}
			if file.ModelSize > 0 {
				parts = append(parts, humanBytes(file.ModelSize))
			}
			if len(parts) > 0 {
				b.WriteString(" (")
				b.WriteString(strings.Join(parts, "; "))
				b.WriteString(")")
			}
			if note := strings.TrimSpace(file.Note); note != "" {
				b.WriteString(": ")
				b.WriteString(truncateUTF8(note, 500))
			}
			b.WriteString("\n")
		}
	}

	if len(design.DesignExtension.DesignPictures) > 0 {
		b.WriteString("\n## Images\n\n")
		for i, picture := range design.DesignExtension.DesignPictures {
			if i >= 5 {
				break
			}
			if strings.TrimSpace(picture.URL) == "" {
				continue
			}
			b.WriteString("- ")
			if strings.TrimSpace(picture.Name) != "" {
				b.WriteString(picture.Name)
				b.WriteString(": ")
			}
			b.WriteString(strings.TrimSpace(picture.URL))
			b.WriteString("\n")
		}
	}

	return strings.TrimSpace(b.String())
}

func writeMakerWorldLine(b *strings.Builder, label string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	b.WriteString(label)
	b.WriteString(": ")
	b.WriteString(value)
	b.WriteString("\n")
}

func writeMakerWorldSection(b *strings.Builder, title string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	b.WriteString("\n## ")
	b.WriteString(title)
	b.WriteString("\n\n")
	b.WriteString(value)
	b.WriteString("\n")
}

func cleanMakerWorldHTML(value string) string {
	return normalizeExtractedText(stripHTMLText(value))
}

func makerWorldNames(values []makerWorldNamedValue) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		name := strings.TrimSpace(value.Name)
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func firstNonEmptyStringSlice(values ...[]string) []string {
	for _, value := range values {
		cleaned := make([]string, 0, len(value))
		for _, entry := range value {
			entry = strings.TrimSpace(entry)
			if entry != "" {
				cleaned = append(cleaned, entry)
			}
		}
		if len(cleaned) > 0 {
			return cleaned
		}
	}
	return nil
}

func makerWorldCreatorText(creator makerWorldCreator) string {
	name := strings.TrimSpace(creator.Name)
	handle := strings.TrimSpace(creator.Handle)
	switch {
	case name != "" && handle != "" && name != handle:
		return name + " (@" + handle + ")"
	case name != "":
		return name
	case handle != "":
		return "@" + handle
	default:
		return ""
	}
}

func makerWorldFilamentText(filaments []makerWorldFilament) string {
	parts := make([]string, 0, len(filaments))
	for _, filament := range filaments {
		label := strings.TrimSpace(filament.Type)
		if label == "" {
			continue
		}
		details := []string{}
		if color := strings.TrimSpace(filament.Color); color != "" {
			details = append(details, color)
		}
		if used := strings.TrimSpace(filament.UsedG); used != "" {
			details = append(details, used+"g")
		}
		if len(details) > 0 {
			label += " (" + strings.Join(details, ", ") + ")"
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, "; ")
}

func humanBytes(size int64) string {
	if size <= 0 {
		return ""
	}
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	value := float64(size)
	for _, suffix := range []string{"KB", "MB", "GB"} {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f TB", value/unit)
}
