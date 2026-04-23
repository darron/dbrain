package youtubeimport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"dbrain/internal/config"
	"dbrain/internal/itemhash"
	"dbrain/internal/model"
	"dbrain/internal/sourceenrich"
	"dbrain/internal/store"
	"dbrain/internal/vault"
)

type Options struct {
	Browser          string
	Profile          string
	Limit            int
	WatchLater       bool
	Liked            bool
	Summarize        bool
	Force            bool
	Transcriber      string
	Model            string
	CLI              string
	Length           string
	Timeout          time.Duration
	Logger           *slog.Logger
	YTDLPBinary      string
	WhisperBinary    string
	WhisperModelPath string
	SummarizeBinary  string
}

type Stats struct {
	FeedsProcessed    int `json:"feeds_processed"`
	ItemsProcessed    int `json:"items_processed"`
	ItemsCreated      int `json:"items_created"`
	ItemsDeleted      int `json:"items_deleted"`
	ItemsUpdated      int `json:"items_updated"`
	ItemsUnchanged    int `json:"items_unchanged"`
	ItemsRendered     int `json:"items_rendered"`
	ItemsSkipped      int `json:"items_skipped"`
	SourcesCreated    int `json:"sources_created"`
	LinksCreated      int `json:"links_created"`
	SourcesQueued     int `json:"sources_queued"`
	SourcesDeleted    int `json:"sources_deleted"`
	SourcesExtracted  int `json:"sources_extracted"`
	SourcesSummarized int `json:"sources_summarized"`
	SourcesRendered   int `json:"sources_rendered"`
	SourcesUnchanged  int `json:"sources_unchanged"`
	Errors            int `json:"errors"`
}

type feed struct {
	name       string
	sourceType string
	url        string
}

type playlistEnvelope struct {
	ID      string       `json:"id"`
	Title   string       `json:"title"`
	Entries []videoEntry `json:"entries"`
}

type videoEntry struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	WebpageURL  string `json:"webpage_url"`
	Description string `json:"description"`
	Uploader    string `json:"uploader"`
	UploaderID  string `json:"uploader_id"`
	Channel     string `json:"channel"`
	ChannelID   string `json:"channel_id"`
	UploadDate  string `json:"upload_date"`
	Timestamp   int64  `json:"timestamp"`
}

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error) {
	if opts.Browser == "" {
		opts.Browser = "chrome"
	}
	if opts.YTDLPBinary == "" {
		opts.YTDLPBinary = "yt-dlp"
	}
	if opts.WhisperBinary == "" {
		opts.WhisperBinary = "whisper-cli"
	}
	if opts.WhisperModelPath == "" {
		opts.WhisperModelPath = defaultWhisperModelPath()
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Minute
	}
	if opts.Length == "" {
		opts.Length = "medium"
	}
	if strings.TrimSpace(opts.Transcriber) == "" {
		opts.Transcriber = "auto"
	}
	if !opts.WatchLater && !opts.Liked {
		opts.WatchLater = true
		opts.Liked = true
	}

	stats := Stats{}
	now := time.Now().UTC()
	touchedSourceIDs := map[int64]struct{}{}

	cleanupStats, err := pruneHistorySignals(ctx, cfg, st)
	if err != nil {
		return stats, err
	}
	stats.ItemsDeleted = cleanupStats.ItemsDeleted
	stats.SourcesDeleted = cleanupStats.SourcesDeleted
	resolvedCookiesArg := cookiesFromBrowserArg(opts.Browser, opts.Profile)

	for _, currentFeed := range selectedFeeds(opts) {
		debugLog(opts.Logger, "loading youtube feed", "feed", currentFeed.name, "url", currentFeed.url)
		envelope, cookiesArg, err := fetchFeed(ctx, currentFeed, opts)
		if err != nil {
			stats.Errors++
			if opts.Logger != nil {
				opts.Logger.Warn("youtube feed load failed", "feed", currentFeed.name, "url", currentFeed.url, "error", err.Error())
			}
			continue
		}
		stats.FeedsProcessed++
		resolvedCookiesArg = cookiesArg

		for _, entry := range envelope.Entries {
			item, skip, err := toItem(entry, currentFeed, now)
			if err != nil {
				return stats, err
			}
			if skip {
				stats.ItemsSkipped++
				continue
			}

			result, err := st.UpsertItem(ctx, item)
			if err != nil {
				return stats, err
			}
			stats.ItemsProcessed++
			switch result.Status {
			case model.UpsertCreated:
				stats.ItemsCreated++
			case model.UpsertUpdated:
				stats.ItemsUpdated++
			case model.UpsertUnchanged:
				stats.ItemsUnchanged++
			}

			shouldRender := result.Status != model.UpsertUnchanged
			if !shouldRender {
				if _, err := vault.StatNote(cfg, item.NotePath); err != nil {
					shouldRender = true
				}
			}
			if shouldRender {
				if err := vault.WriteItem(cfg, item); err != nil {
					return stats, fmt.Errorf("render note %s: %w", item.SourceKey, err)
				}
				stats.ItemsRendered++
			}

			candidate := sourceCandidateForVideo(item.CanonicalURL)
			linkResult, err := st.UpsertSourceLink(ctx, result.ItemID, candidate)
			if err != nil {
				return stats, err
			}
			if linkResult.SourceCreated {
				stats.SourcesCreated++
			}
			if linkResult.LinkCreated {
				stats.LinksCreated++
			}
			touchedSourceIDs[linkResult.SourceID] = struct{}{}
		}
	}

	if len(touchedSourceIDs) == 0 {
		return stats, nil
	}

	enrichStats, _, err := sourceenrich.RunSourceIDs(ctx, cfg, st, mapKeys(touchedSourceIDs), sourceenrich.Options{
		Force:              opts.Force,
		Summarize:          opts.Summarize,
		Model:              opts.Model,
		CLI:                opts.CLI,
		Length:             opts.Length,
		Timeout:            opts.Timeout,
		Logger:             opts.Logger,
		Binary:             opts.SummarizeBinary,
		EnvFor:             summarizeEnvFor(resolvedCookiesArg),
		ArgsFor:            summarizeArgsFor(opts),
		FallbackExtractFor: fallbackExtractFor(cfg, opts, resolvedCookiesArg),
	})
	if err != nil {
		return stats, err
	}

	stats.SourcesQueued = enrichStats.SourcesQueued
	stats.SourcesExtracted = enrichStats.SourcesExtracted
	stats.SourcesSummarized = enrichStats.SourcesSummarized
	stats.SourcesRendered = enrichStats.SourcesRendered
	stats.SourcesUnchanged = enrichStats.SourcesUnchanged
	stats.Errors += enrichStats.Errors

	return stats, nil
}

func selectedFeeds(opts Options) []feed {
	feeds := make([]feed, 0, 2)
	if opts.WatchLater {
		feeds = append(feeds, feed{
			name:       "watch_later",
			sourceType: "youtube_watch_later",
			url:        "https://www.youtube.com/playlist?list=WL",
		})
	}
	if opts.Liked {
		feeds = append(feeds, feed{
			name:       "liked",
			sourceType: "youtube_liked",
			url:        "https://www.youtube.com/playlist?list=LL",
		})
	}
	return feeds
}

type cleanupStats struct {
	ItemsDeleted   int
	SourcesDeleted int
}

func pruneHistorySignals(ctx context.Context, cfg config.Config, st *store.Store) (cleanupStats, error) {
	itemResult, err := st.DeleteItemsBySourceType(ctx, "youtube_history")
	if err != nil {
		return cleanupStats{}, err
	}
	if err := removeNoteFiles(cfg, itemResult.NotePaths); err != nil {
		return cleanupStats{}, err
	}

	sourceResult, err := st.DeleteOrphanSources(ctx, "youtube")
	if err != nil {
		return cleanupStats{}, err
	}
	if err := removeNoteFiles(cfg, sourceResult.NotePaths); err != nil {
		return cleanupStats{}, err
	}

	return cleanupStats{
		ItemsDeleted:   itemResult.Count,
		SourcesDeleted: sourceResult.Count,
	}, nil
}

func removeNoteFiles(cfg config.Config, notePaths []string) error {
	for _, notePath := range notePaths {
		notePath = strings.TrimSpace(notePath)
		if notePath == "" {
			continue
		}
		absolute := filepath.Join(cfg.VaultDir, filepath.FromSlash(notePath))
		if err := os.Remove(absolute); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove note %s: %w", absolute, err)
		}
	}
	return nil
}

func fetchFeed(ctx context.Context, currentFeed feed, opts Options) (playlistEnvelope, string, error) {
	cookieArgs := cookiesFromBrowserArgs(opts.Browser, opts.Profile)
	var lastErr error

	for _, cookiesArg := range cookieArgs {
		commandCtx, cancel := context.WithTimeout(ctx, opts.Timeout)

		args := []string{"--dump-single-json", "--flat-playlist", "--cookies-from-browser", cookiesArg}
		if opts.Limit > 0 {
			args = append(args, "--playlist-end", fmt.Sprintf("%d", opts.Limit))
		}
		args = append(args, currentFeed.url)

		cmd := exec.CommandContext(commandCtx, opts.YTDLPBinary, args...)
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		cancel()
		if err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = err.Error()
			}
			lastErr = fmt.Errorf("run yt-dlp for %s with %s: %s", currentFeed.name, cookiesArg, msg)
			debugLog(opts.Logger, "youtube feed load attempt failed", "feed", currentFeed.name, "url", currentFeed.url, "cookies", cookiesArg, "error", msg)
			continue
		}

		var envelope playlistEnvelope
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			lastErr = fmt.Errorf("parse yt-dlp json for %s with %s: %w", currentFeed.name, cookiesArg, err)
			debugLog(opts.Logger, "youtube feed parse failed", "feed", currentFeed.name, "url", currentFeed.url, "cookies", cookiesArg, "error", err.Error())
			continue
		}

		debugLog(opts.Logger, "youtube feed loaded", "feed", currentFeed.name, "url", currentFeed.url, "cookies", cookiesArg, "entries", len(envelope.Entries))
		return envelope, cookiesArg, nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("run yt-dlp for %s: no cookie candidates available", currentFeed.name)
	}
	return playlistEnvelope{}, "", lastErr
}

func toItem(entry videoEntry, currentFeed feed, now time.Time) (model.Item, bool, error) {
	videoID := strings.TrimSpace(entry.ID)
	if videoID == "" {
		return model.Item{}, true, nil
	}

	title := strings.TrimSpace(entry.Title)
	if title == "" {
		title = "YouTube video " + videoID
	}

	watchURL := canonicalVideoURL(entry)
	publishedAt := normalizeYouTubeTimestamp(entry)
	signal := currentFeed.name

	payload := map[string]any{
		"feed": map[string]string{
			"name": currentFeed.name,
			"url":  currentFeed.url,
		},
		"video": entry,
	}
	rawJSONBytes, err := json.Marshal(payload)
	if err != nil {
		return model.Item{}, false, fmt.Errorf("marshal youtube item %s: %w", videoID, err)
	}

	item := model.Item{
		SourceKey:       "yt:" + signal + ":" + videoID,
		SourceType:      currentFeed.sourceType,
		ExternalID:      videoID,
		CanonicalURL:    watchURL,
		Title:           title,
		AuthorHandle:    firstNonEmpty(strings.TrimSpace(entry.UploaderID), strings.TrimSpace(entry.ChannelID)),
		AuthorName:      firstNonEmpty(strings.TrimSpace(entry.Channel), strings.TrimSpace(entry.Uploader)),
		PublishedAt:     publishedAt,
		SavedAt:         "",
		SyncedAt:        "",
		Text:            strings.TrimSpace(entry.Description),
		PrimaryCategory: signal,
		PrimaryDomain:   "youtube.com",
		Categories:      signal,
		LinksJSON:       "[]",
		NotePath:        vault.NoteRelativePath(filepath.ToSlash(filepath.Join("youtube", signal)), chooseYear(publishedAt, now.Format(time.RFC3339)), videoID),
		RawJSON:         string(rawJSONBytes),
		ImportedAt:      now,
		UpdatedAt:       now,
		LastSeenAt:      now,
	}
	item.ContentHash = itemhash.Compute(item)

	return item, false, nil
}

func sourceCandidateForVideo(rawURL string) model.SourceCandidate {
	canonical := canonicalizeVideoURL(rawURL)
	hash := shortHash(canonical)
	return model.SourceCandidate{
		OriginalURL:   rawURL,
		CanonicalURL:  canonical,
		NormalizedURL: canonical,
		SourceType:    "youtube",
		Domain:        "youtube.com",
		SourceKey:     "src:" + hash,
		NotePath:      vault.SourceNoteRelativePath("youtube", "youtube-"+hash),
	}
}

func summarizeEnvFor(cookies string) func(source model.SourceDocument) map[string]string {
	return func(source model.SourceDocument) map[string]string {
		if source.SourceType != "youtube" {
			return nil
		}
		return map[string]string{
			"SUMMARIZE_YT_DLP_COOKIES_FROM_BROWSER": cookies,
		}
	}
}

func summarizeArgsFor(opts Options) func(source model.SourceDocument) []string {
	return func(source model.SourceDocument) []string {
		if source.SourceType != "youtube" {
			return nil
		}
		args := []string{"--youtube", "auto", "--video-mode", "transcript"}
		if value := strings.TrimSpace(opts.Transcriber); value != "" {
			args = append(args, "--transcriber", value)
		}
		return args
	}
}

func fallbackExtractFor(cfg config.Config, opts Options, cookiesArg string) func(ctx context.Context, source model.SourceDocument, extract model.ExtractResult) (model.ExtractResult, bool, error) {
	return func(ctx context.Context, source model.SourceDocument, extract model.ExtractResult) (model.ExtractResult, bool, error) {
		if source.SourceType != "youtube" {
			return model.ExtractResult{}, false, nil
		}
		if !shouldUseWhisperFallback(extract) {
			return model.ExtractResult{}, false, nil
		}
		debugLog(opts.Logger, "attempting whisper fallback for youtube source", "source_key", source.SourceKey, "url", source.CanonicalURL)
		fallback, err := transcribeYouTubeWithWhisper(ctx, cfg, source, extract, opts, cookiesArg)
		if err != nil {
			debugLog(opts.Logger, "whisper fallback failed", "source_key", source.SourceKey, "url", source.CanonicalURL, "error", err.Error())
			return model.ExtractResult{}, false, nil
		}
		debugLog(opts.Logger, "whisper fallback succeeded", "source_key", source.SourceKey, "url", source.CanonicalURL, "content_chars", len(fallback.Content))
		return fallback, true, nil
	}
}

type youtubeExtractEnvelope struct {
	Extracted struct {
		TranscriptSource      *string `json:"transcriptSource"`
		TranscriptionProvider *string `json:"transcriptionProvider"`
		TranscriptCharacters  *int    `json:"transcriptCharacters"`
	} `json:"extracted"`
}

func shouldUseWhisperFallback(extract model.ExtractResult) bool {
	var payload youtubeExtractEnvelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(extract.RawJSON)), &payload); err != nil {
		return false
	}

	source := ""
	if payload.Extracted.TranscriptSource != nil {
		source = strings.TrimSpace(*payload.Extracted.TranscriptSource)
	}
	provider := ""
	if payload.Extracted.TranscriptionProvider != nil {
		provider = strings.TrimSpace(*payload.Extracted.TranscriptionProvider)
	}
	chars := 0
	if payload.Extracted.TranscriptCharacters != nil {
		chars = *payload.Extracted.TranscriptCharacters
	}

	return source == "unavailable" && provider == "" && chars == 0
}

func transcribeYouTubeWithWhisper(ctx context.Context, cfg config.Config, source model.SourceDocument, extract model.ExtractResult, opts Options, cookiesArg string) (model.ExtractResult, error) {
	if strings.TrimSpace(opts.WhisperModelPath) == "" {
		return model.ExtractResult{}, fmt.Errorf("whisper model path not configured")
	}
	if _, err := os.Stat(opts.WhisperModelPath); err != nil {
		return model.ExtractResult{}, fmt.Errorf("whisper model missing: %w", err)
	}

	timeout := opts.Timeout
	if timeout < 10*time.Minute {
		timeout = 10 * time.Minute
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tempDir, err := cfg.MkdirTemp("dbrain-youtube-whisper-*")
	if err != nil {
		return model.ExtractResult{}, fmt.Errorf("create temp dir: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	audioTemplate := filepath.Join(tempDir, "audio.%(ext)s")
	downloadArgs := []string{
		"--no-playlist",
		"--cookies-from-browser", firstNonEmpty(strings.TrimSpace(cookiesArg), cookiesFromBrowserArg(opts.Browser, opts.Profile)),
		"-f", "bestaudio/best",
		"-o", audioTemplate,
		source.CanonicalURL,
	}
	downloadCmd := exec.CommandContext(commandCtx, opts.YTDLPBinary, downloadArgs...)
	var downloadStderr bytes.Buffer
	downloadCmd.Stderr = &downloadStderr
	if err := downloadCmd.Run(); err != nil {
		return model.ExtractResult{}, fmt.Errorf("yt-dlp audio download: %s", strings.TrimSpace(downloadStderr.String()))
	}

	audioPath, err := firstDownloadedAudio(tempDir)
	if err != nil {
		return model.ExtractResult{}, err
	}

	outputBase := filepath.Join(tempDir, "transcript")
	whisperArgs := []string{
		"-m", opts.WhisperModelPath,
		"-l", "auto",
		"-otxt",
		"-of", outputBase,
		"-f", audioPath,
		"-np",
		"-nt",
	}
	whisperCmd := exec.CommandContext(commandCtx, opts.WhisperBinary, whisperArgs...)
	var whisperStderr bytes.Buffer
	whisperCmd.Stderr = &whisperStderr
	if err := whisperCmd.Run(); err != nil {
		return model.ExtractResult{}, fmt.Errorf("whisper transcription: %s", strings.TrimSpace(whisperStderr.String()))
	}

	transcriptBytes, err := os.ReadFile(outputBase + ".txt")
	if err != nil {
		return model.ExtractResult{}, fmt.Errorf("read whisper transcript: %w", err)
	}
	transcript := strings.TrimSpace(string(transcriptBytes))
	if transcript == "" {
		return model.ExtractResult{}, fmt.Errorf("whisper transcript empty")
	}

	title := strings.TrimSpace(extract.Title)
	if title == "" || title == "- YouTube" {
		title = strings.TrimSpace(source.Title)
	}
	description := strings.TrimSpace(extract.Description)
	if description == "" {
		description = strings.TrimSpace(source.Description)
	}
	siteName := strings.TrimSpace(extract.SiteName)
	if siteName == "" || siteName == "youtube.com" {
		siteName = "YouTube"
	}

	rawJSONBytes, err := json.Marshal(map[string]any{
		"extracted": map[string]any{
			"url":                   source.CanonicalURL,
			"title":                 title,
			"description":           description,
			"siteName":              siteName,
			"content":               "Transcript:\n" + transcript,
			"transcriptSource":      "whisper.cpp",
			"transcriptionProvider": "whisper.cpp",
			"transcriptCharacters":  len(transcript),
		},
	})
	if err != nil {
		return model.ExtractResult{}, fmt.Errorf("marshal whisper transcript json: %w", err)
	}

	return model.ExtractResult{
		CanonicalURL: source.CanonicalURL,
		FinalURL:     source.CanonicalURL,
		Title:        title,
		Description:  description,
		SiteName:     siteName,
		Content:      "Transcript:\n" + transcript,
		RawJSON:      string(rawJSONBytes),
		Status:       "ok",
		FetchedAt:    time.Now().UTC(),
		Tool:         "whisper.cpp",
		ToolVersion:  "",
	}, nil
}

func firstDownloadedAudio(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read audio dir: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".part") || strings.HasSuffix(name, ".ytdl") || strings.HasSuffix(name, ".txt") {
			continue
		}
		return filepath.Join(dir, name), nil
	}
	return "", fmt.Errorf("downloaded audio not found")
}

func defaultWhisperModelPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".summarize", "cache", "whisper-cpp", "models", "ggml-base.bin")
}

func cookiesFromBrowserArg(browser, profile string) string {
	browser = strings.TrimSpace(browser)
	if browser == "" {
		browser = "chrome"
	}
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return browser
	}
	return browser + ":" + profile
}

func cookiesFromBrowserArgs(browser, profile string) []string {
	primary := cookiesFromBrowserArg(browser, profile)
	seen := map[string]struct{}{}
	out := make([]string, 0, 8)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}

	add(primary)
	if strings.TrimSpace(profile) != "" {
		return out
	}

	for _, discovered := range discoverBrowserProfiles(browser) {
		add(browser + ":" + discovered)
	}
	for _, fallback := range []string{"Default", "Profile 1", "Profile 2", "Profile 3", "Profile 4", "Profile 5", "Profile 6"} {
		add(browser + ":" + fallback)
	}
	return out
}

func discoverBrowserProfiles(browser string) []string {
	baseDir := browserProfileBaseDir(browser)
	if baseDir == "" {
		return nil
	}

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil
	}

	profiles := make([]string, 0, 8)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if name == "Default" || strings.HasPrefix(name, "Profile ") {
			profiles = append(profiles, name)
		}
	}
	sort.Slice(profiles, func(i, j int) bool {
		if profiles[i] == "Default" {
			return true
		}
		if profiles[j] == "Default" {
			return false
		}
		return profileSortKey(profiles[i]) < profileSortKey(profiles[j])
	})
	return profiles
}

func browserProfileBaseDir(browser string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	switch runtime.GOOS {
	case "darwin":
		switch strings.ToLower(strings.TrimSpace(browser)) {
		case "chrome":
			return filepath.Join(home, "Library", "Application Support", "Google", "Chrome")
		case "chromium":
			return filepath.Join(home, "Library", "Application Support", "Chromium")
		case "brave":
			return filepath.Join(home, "Library", "Application Support", "BraveSoftware", "Brave-Browser")
		case "edge":
			return filepath.Join(home, "Library", "Application Support", "Microsoft Edge")
		case "vivaldi":
			return filepath.Join(home, "Library", "Application Support", "Vivaldi")
		case "opera":
			return filepath.Join(home, "Library", "Application Support", "com.operasoftware.Opera")
		}
	case "linux":
		switch strings.ToLower(strings.TrimSpace(browser)) {
		case "chrome":
			return filepath.Join(home, ".config", "google-chrome")
		case "chromium":
			return filepath.Join(home, ".config", "chromium")
		case "brave":
			return filepath.Join(home, ".config", "BraveSoftware", "Brave-Browser")
		case "edge":
			return filepath.Join(home, ".config", "microsoft-edge")
		case "vivaldi":
			return filepath.Join(home, ".config", "vivaldi")
		case "opera":
			return filepath.Join(home, ".config", "opera")
		}
	}

	return ""
}

func profileSortKey(value string) int {
	if value == "Default" {
		return -1
	}
	trimmed := strings.TrimPrefix(value, "Profile ")
	number, err := strconv.Atoi(trimmed)
	if err != nil {
		return 1 << 30
	}
	return number
}

func canonicalVideoURL(entry videoEntry) string {
	if value := strings.TrimSpace(entry.WebpageURL); value != "" {
		return canonicalizeVideoURL(value)
	}
	if value := strings.TrimSpace(entry.URL); value != "" {
		if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
			return canonicalizeVideoURL(value)
		}
		return canonicalizeVideoURL("https://www.youtube.com/watch?v=" + value)
	}
	return canonicalizeVideoURL("https://www.youtube.com/watch?v=" + strings.TrimSpace(entry.ID))
}

func canonicalizeVideoURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw
	}

	host := strings.ToLower(u.Hostname())
	switch host {
	case "youtu.be":
		videoID := strings.Trim(strings.TrimSpace(u.Path), "/")
		if videoID != "" {
			u = &url.URL{
				Scheme: "https",
				Host:   "www.youtube.com",
				Path:   "/watch",
			}
			query := url.Values{}
			query.Set("v", videoID)
			u.RawQuery = query.Encode()
			return u.String()
		}
	case "youtube.com", "www.youtube.com", "m.youtube.com":
		query := u.Query()
		videoID := strings.TrimSpace(query.Get("v"))
		if strings.HasPrefix(strings.TrimSpace(u.Path), "/shorts/") {
			videoID = strings.Trim(strings.TrimPrefix(strings.TrimSpace(u.Path), "/shorts/"), "/")
		}
		if videoID != "" {
			clean := &url.URL{
				Scheme: "https",
				Host:   "www.youtube.com",
				Path:   "/watch",
			}
			values := url.Values{}
			values.Set("v", videoID)
			clean.RawQuery = values.Encode()
			return clean.String()
		}
	}
	u.Scheme = "https"
	u.Fragment = ""
	return strings.TrimSuffix(u.String(), "?")
}

func normalizeYouTubeTimestamp(entry videoEntry) string {
	if entry.Timestamp > 0 {
		return time.Unix(entry.Timestamp, 0).UTC().Format(time.RFC3339)
	}
	value := strings.TrimSpace(entry.UploadDate)
	if len(value) == 8 {
		if t, err := time.Parse("20060102", value); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return ""
}

func chooseYear(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, value); err == nil {
			return t.Format("2006")
		}
	}
	return "unknown"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

func mapKeys(values map[int64]struct{}) []int64 {
	out := make([]int64, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func debugLog(logger *slog.Logger, msg string, args ...any) {
	if logger == nil {
		return
	}
	logger.Debug(msg, args...)
}
