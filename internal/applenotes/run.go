package applenotes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/itemhash"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/summarizecli"
	"github.com/darron/dbrain/internal/summaryconfig"
	"github.com/darron/dbrain/internal/vault"
)

const sourceType = "apple_note"
const appleNoteSummaryPromptVersion = "apple-notes-summary-v2"
const appleNoteSummaryPrompt = "Summarize this Apple Note for personal second-brain retrieval. Start with a note shape label: authored_note, research_link_list, shopping_or_checklist, meeting_or_log, scratchpad, or mixed. Preserve personal framing, distinguish tasks/ideas/meeting notes/copied excerpts/topical link collections when clear, treat rough lists as collected context rather than polished claims, do not invent certainty, and write a concise plain-text summary without markdown headings."

type Stats struct {
	SourceDBPath         string       `json:"source_db_path"`
	Snapshot             SnapshotInfo `json:"snapshot"`
	NotesSeen            int          `json:"notes_seen"`
	NotesMatched         int          `json:"notes_matched"`
	NotesImported        int          `json:"notes_imported"`
	NotesCreated         int          `json:"notes_created"`
	NotesUpdated         int          `json:"notes_updated"`
	NotesUnchanged       int          `json:"notes_unchanged"`
	NotesRendered        int          `json:"notes_rendered"`
	NotesSkipped         int          `json:"notes_skipped"`
	NotesBlocked         int          `json:"notes_blocked"`
	NotesPurged          int          `json:"notes_purged"`
	ExcludedAccounts     int          `json:"excluded_accounts"`
	ExcludedFolders      int          `json:"excluded_folders"`
	ExcludedShared       int          `json:"excluded_shared"`
	BlockedLocked        int          `json:"blocked_locked"`
	BlockedEmpty         int          `json:"blocked_empty"`
	AttachmentsSeen      int          `json:"attachments_seen"`
	AttachmentsIndexed   int          `json:"attachments_indexed"`
	AttachmentsExtracted int          `json:"attachments_extracted"`
	AttachmentsOCRed     int          `json:"attachments_ocred"`
	AttachmentsBlocked   int          `json:"attachments_blocked"`
	LinksDiscovered      int          `json:"links_discovered"`
	SummariesCreated     int          `json:"summaries_created"`
	SummaryErrors        int          `json:"summary_errors"`
	SampleTitles         []string     `json:"sample_titles,omitempty"`
	DryRun               bool         `json:"dry_run"`
	Applied              bool         `json:"applied"`
	Errors               int          `json:"errors"`
}

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error) {
	readOpts := opts
	deferAttachmentEnrichment := false
	if !opts.DryRun && opts.Limit > 0 {
		// In applied mode, --limit is a work limit. Read all candidate notes so
		// unchanged-current rows do not consume the batch and repeated runs advance.
		readOpts.Limit = 0
		if !opts.SkipAttachments {
			readOpts.SkipAttachments = true
			deferAttachmentEnrichment = true
		}
	}
	docs, snapshot, err := ReadDocuments(ctx, cfg, readOpts)
	if err != nil {
		return Stats{}, err
	}

	stats := Stats{
		SourceDBPath: snapshot.SourceDBPath,
		Snapshot:     snapshot,
		DryRun:       opts.DryRun,
		Applied:      !opts.DryRun,
	}
	now := time.Now().UTC()
	emitProgress(opts, ProgressEvent{Phase: "loaded", Total: len(docs)})

	processedWork := 0
	for index, doc := range docs {
		stats.NotesSeen++
		event := ProgressEvent{
			Index:           index + 1,
			Total:           len(docs),
			SourceKey:       doc.SourceKey,
			Title:           doc.Title,
			Links:           len(doc.Links),
			Attachments:     len(doc.Attachments),
			TextChars:       len(doc.Text),
			AttachmentChars: totalAttachmentTextChars(doc),
		}
		if skipReason := exclusionReason(doc, opts); skipReason != "" {
			countAttachments(&stats, doc.Attachments)
			if opts.ForgetExcluded && stats.Applied {
				purged, err := purgeExcluded(ctx, cfg, st, doc, skipReason)
				if err != nil {
					stats.Errors++
					return stats, err
				}
				if purged {
					stats.NotesPurged++
				}
			}
			countSkip(&stats, skipReason)
			event.Phase = "skipped"
			event.Reason = skipReason
			emitProgress(opts, event)
			continue
		}
		if doc.BlockedReason != "" {
			countAttachments(&stats, doc.Attachments)
			countSkip(&stats, doc.BlockedReason)
			event.Phase = "blocked"
			event.Reason = doc.BlockedReason
			emitProgress(opts, event)
			continue
		}
		if containsIgnoreMarker(doc.Text) {
			countAttachments(&stats, doc.Attachments)
			if opts.ForgetExcluded && stats.Applied {
				purged, err := purgeExcluded(ctx, cfg, st, doc, "ignore_marker")
				if err != nil {
					stats.Errors++
					return stats, err
				}
				if purged {
					stats.NotesPurged++
				}
			}
			stats.NotesSkipped++
			event.Phase = "skipped"
			event.Reason = "ignore_marker"
			emitProgress(opts, event)
			continue
		}

		stats.NotesMatched++
		stats.LinksDiscovered += len(doc.Links)
		if stats.DryRun && opts.ShowTitles {
			stats.SampleTitles = appendSampleTitle(stats.SampleTitles, doc.Title)
		}
		if stats.DryRun {
			countAttachments(&stats, doc.Attachments)
			event.Phase = "dry_run"
			event.Status = "would_import"
			emitProgress(opts, event)
			continue
		}
		if st == nil {
			return stats, fmt.Errorf("store is required for Apple Notes import")
		}

		item, err := itemFromDocument(doc, now)
		if err != nil {
			stats.Errors++
			return stats, err
		}
		plan, err := planAppleNoteWork(ctx, cfg, st, opts, item)
		if err != nil {
			stats.Errors++
			return stats, err
		}
		if opts.Limit > 0 && !plan.Actionable {
			countAttachments(&stats, doc.Attachments)
			stats.NotesUnchanged++
			event.Phase = "unchanged"
			event.Status = "current"
			emitProgress(opts, event)
			continue
		}
		if opts.Limit > 0 && processedWork >= opts.Limit {
			break
		}
		if deferAttachmentEnrichment {
			enriched, err := enrichSingleDocumentAttachments(ctx, cfg, doc, opts, snapshot.SourceDBPath)
			if err != nil {
				stats.Errors++
				return stats, err
			}
			doc = enriched
			item, err = itemFromDocument(doc, now)
			if err != nil {
				stats.Errors++
				return stats, err
			}
			plan, err = planAppleNoteWork(ctx, cfg, st, opts, item)
			if err != nil {
				stats.Errors++
				return stats, err
			}
			event.Links = len(doc.Links)
			event.Attachments = len(doc.Attachments)
			event.TextChars = len(doc.Text)
			event.AttachmentChars = totalAttachmentTextChars(doc)
			if opts.Limit > 0 && !plan.Actionable {
				countAttachments(&stats, doc.Attachments)
				stats.NotesUnchanged++
				event.Phase = "unchanged"
				event.Status = "current"
				emitProgress(opts, event)
				continue
			}
		}
		countAttachments(&stats, doc.Attachments)
		processedWork++

		event.Phase = "processing"
		if plan.Reason != "" {
			event.Reason = plan.Reason
		}
		emitProgress(opts, event)

		result, err := st.UpsertItem(ctx, item)
		if err != nil {
			stats.Errors++
			return stats, err
		}
		stats.NotesImported++
		switch result.Status {
		case model.UpsertCreated:
			stats.NotesCreated++
		case model.UpsertUpdated:
			stats.NotesUpdated++
		case model.UpsertUnchanged:
			stats.NotesUnchanged++
		}

		shouldRender := opts.Force || result.Status != model.UpsertUnchanged || plan.RenderNeeded
		if !shouldRender {
			if _, err := vault.StatNote(cfg, item.NotePath); err != nil {
				shouldRender = true
			}
		}
		if shouldRender {
			if err := vault.WriteItem(cfg, item); err != nil {
				stats.Errors++
				return stats, fmt.Errorf("render apple note %s: %w", item.SourceKey, err)
			}
			stats.NotesRendered++
		}
		event.Phase = "imported"
		event.Status = string(result.Status)
		event.Rendered = shouldRender
		event.SummaryStatus = "skipped"
		if opts.Summarize {
			event.Phase = "summarizing"
			event.Status = string(result.Status)
			event.Rendered = shouldRender
			event.SummaryStatus = "running"
			emitProgress(opts, event)

			summarized, err := summarizeAppleNote(ctx, cfg, st, opts, result.ItemID, item)
			if err != nil {
				stats.SummaryErrors++
				stats.Errors++
				event.Phase = "imported"
				event.SummaryStatus = "error"
				event.Reason = err.Error()
				emitProgress(opts, event)
				continue
			}
			if summarized {
				stats.SummariesCreated++
				stats.NotesRendered++
				event.SummaryStatus = "ok"
			} else {
				event.SummaryStatus = "current"
			}
		}
		event.Phase = "imported"
		emitProgress(opts, event)
	}

	return stats, nil
}

type appleNoteWorkPlan struct {
	Actionable    bool
	RenderNeeded  bool
	SummaryNeeded bool
	Reason        string
}

func planAppleNoteWork(ctx context.Context, cfg config.Config, st *store.Store, opts Options, item model.Item) (appleNoteWorkPlan, error) {
	if opts.Force {
		return appleNoteWorkPlan{Actionable: true, RenderNeeded: true, SummaryNeeded: opts.Summarize, Reason: "force"}, nil
	}
	current, err := st.GetItem(ctx, item.SourceKey)
	if err != nil {
		if isItemNotFound(err) {
			return appleNoteWorkPlan{Actionable: true, RenderNeeded: true, SummaryNeeded: opts.Summarize, Reason: "new"}, nil
		}
		return appleNoteWorkPlan{}, err
	}

	plan := appleNoteWorkPlan{}
	if current.ContentHash != item.ContentHash {
		plan.Actionable = true
		plan.Reason = appendReason(plan.Reason, "changed")
	}
	if _, err := vault.StatNote(cfg, item.NotePath); err != nil {
		plan.Actionable = true
		plan.RenderNeeded = true
		plan.Reason = appendReason(plan.Reason, "missing_render")
	}
	if opts.Summarize && appleNoteSummaryNeeded(current, item) {
		plan.Actionable = true
		plan.SummaryNeeded = true
		plan.Reason = appendReason(plan.Reason, "summary")
	}
	return plan, nil
}

func appleNoteSummaryNeeded(current model.Item, item model.Item) bool {
	input := appleNoteSummaryInput(item)
	if input == "" {
		return false
	}
	inputHash := hashSummaryInput(input)
	return current.SummaryStatus != "ok" ||
		strings.TrimSpace(current.SummaryText) == "" ||
		strings.TrimSpace(current.SummaryInputHash) != inputHash ||
		strings.TrimSpace(current.SummaryPromptVersion) != appleNoteSummaryPromptVersion
}

func isItemNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "item not found:")
}

func appendReason(current string, next string) string {
	if current == "" {
		return next
	}
	return current + "," + next
}

func emitProgress(opts Options, event ProgressEvent) {
	if opts.Progress == nil {
		return
	}
	opts.Progress(event)
}

func purgeExcluded(ctx context.Context, cfg config.Config, st *store.Store, doc NoteDocument, reason string) (bool, error) {
	if st == nil {
		return false, nil
	}
	raw, err := json.Marshal(map[string]any{
		"source_type":  sourceType,
		"source_key":   doc.SourceKey,
		"external_id":  doc.ExternalID,
		"purged":       true,
		"purge_reason": reason,
		"purged_at":    time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return false, err
	}
	purged, err := st.PurgeItemIndexedContent(ctx, doc.SourceKey, string(raw))
	if err != nil || !purged {
		return purged, err
	}
	item, err := st.GetItem(ctx, doc.SourceKey)
	if err != nil {
		return false, err
	}
	if err := vault.WriteItem(cfg, item); err != nil {
		return false, err
	}
	return true, nil
}

func summarizeAppleNote(ctx context.Context, cfg config.Config, st *store.Store, opts Options, itemID int64, item model.Item) (bool, error) {
	input := appleNoteSummaryInput(item)
	if input == "" {
		return false, nil
	}
	inputHash := hashSummaryInput(input)
	if !opts.Force {
		current, err := st.GetItem(ctx, item.SourceKey)
		if err != nil {
			return false, err
		}
		if current.SummaryStatus == "ok" &&
			strings.TrimSpace(current.SummaryText) != "" &&
			strings.TrimSpace(current.SummaryInputHash) == inputHash &&
			strings.TrimSpace(current.SummaryPromptVersion) == appleNoteSummaryPromptVersion {
			return false, nil
		}
	}
	modelName := summaryconfig.Model(cfg.RootDir, opts.SummaryModel)
	length := strings.TrimSpace(opts.SummaryLength)
	if length == "" {
		length = "medium"
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}

	runResult, err := summarizecli.Run(ctx, summarizecli.Options{
		Summarize: true,
		Stdin:     input,
		Model:     modelName,
		CLI:       opts.SummaryCLI,
		Length:    length,
		Timeout:   timeout,
		Prompt:    appleNoteSummaryPrompt,
		RootDir:   cfg.RootDir,
	})

	summary := model.SummaryResult{
		Model:         modelName,
		PromptVersion: appleNoteSummaryPromptVersion,
		Status:        "ok",
		FetchedAt:     time.Now().UTC(),
	}
	if err != nil {
		summary.Status = "error"
		summary.Error = err.Error()
		summary.Tool = summarizecli.SummaryToolName(modelName)
		summary.ToolVersion = summarizecli.SummaryToolVersion(ctx, "summarize", modelName)
	} else {
		summary = runResult.Summary
		summary.PromptVersion = appleNoteSummaryPromptVersion
	}

	changed, err := st.SaveItemSummary(ctx, itemID, summary, inputHash)
	if err != nil {
		return false, err
	}
	if summary.Status != "ok" {
		return false, fmt.Errorf("summarize apple note %s: %s", item.SourceKey, summary.Error)
	}
	if !changed {
		return false, nil
	}
	refreshed, err := st.GetItem(ctx, item.SourceKey)
	if err != nil {
		return false, err
	}
	if err := vault.WriteItem(cfg, refreshed); err != nil {
		return false, err
	}
	return true, nil
}

func appleNoteSummaryInput(item model.Item) string {
	var b strings.Builder
	if text := strings.TrimSpace(item.Text); text != "" {
		b.WriteString("Apple Note Body:\n")
		b.WriteString(text)
	}
	if attachmentText := strings.TrimSpace(item.ArticleText); attachmentText != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("Apple Note Attachments:\n")
		b.WriteString(attachmentText)
	}
	return strings.TrimSpace(b.String())
}

func hashSummaryInput(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}

func appendSampleTitle(titles []string, title string) []string {
	if len(titles) >= 20 {
		return titles
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Untitled Apple Note"
	}
	return append(titles, title)
}

func itemFromDocument(doc NoteDocument, now time.Time) (model.Item, error) {
	linksJSON, err := json.Marshal(doc.Links)
	if err != nil {
		return model.Item{}, fmt.Errorf("encode apple note links: %w", err)
	}
	raw := map[string]any{
		"account_name":       doc.AccountName,
		"folder_path":        doc.FolderPath,
		"apple_note_tags":    doc.AppleNoteTags,
		"blocked_reason":     doc.BlockedReason,
		"password_protected": doc.PasswordProtected,
		"shared":             doc.Shared,
		"attachments":        doc.Attachments,
		"attachment_texts":   doc.AttachmentTexts,
		"raw":                doc.Raw,
	}
	rawJSON, err := json.Marshal(raw)
	if err != nil {
		return model.Item{}, fmt.Errorf("encode apple note raw metadata: %w", err)
	}

	year := "unknown"
	if doc.CreatedAt != "" {
		if parsed, err := time.Parse(time.RFC3339, doc.CreatedAt); err == nil {
			year = fmt.Sprintf("%04d", parsed.Year())
		}
	}
	noteSlug := noteSlug(doc.Title, doc.ExternalID)
	item := model.Item{
		SourceKey:    doc.SourceKey,
		SourceType:   sourceType,
		ExternalID:   doc.ExternalID,
		CanonicalURL: doc.CanonicalURL,
		Title:        doc.Title,
		PublishedAt:  doc.CreatedAt,
		SavedAt:      doc.CreatedAt,
		SyncedAt:     now.Format(time.RFC3339),
		Text:         doc.Text,
		ArticleTitle: attachmentArticleTitle(doc),
		ArticleText:  attachmentArticleText(doc),
		LinksJSON:    string(linksJSON),
		FolderNames:  doc.FolderPath,
		NotePath:     vault.NoteRelativePath("apple-notes", year, noteSlug),
		RawJSON:      string(rawJSON),
		LastSeenAt:   now,
		UpdatedAt:    now,
	}
	item.ContentHash = itemhash.Compute(item)
	return item, nil
}

func attachmentArticleTitle(doc NoteDocument) string {
	if len(doc.AttachmentTexts) == 0 && len(doc.Attachments) == 0 {
		return ""
	}
	return "Apple Notes Attachment Text"
}

func attachmentArticleText(doc NoteDocument) string {
	var b strings.Builder
	for _, text := range doc.AttachmentTexts {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(text)
	}
	for _, attachment := range doc.Attachments {
		metadata := renderAttachmentMetadata(attachment)
		if metadata == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(metadata)
	}
	return strings.TrimSpace(b.String())
}

func renderAttachmentMetadata(attachment Attachment) string {
	var lines []string
	if attachment.Name != "" {
		lines = append(lines, "Name: "+attachment.Name)
	}
	if attachment.FileName != "" {
		lines = append(lines, "File: "+attachment.FileName)
	}
	if attachment.URL != "" {
		lines = append(lines, "URL: "+attachment.URL)
	}
	if attachment.MIMEType != "" {
		lines = append(lines, "MIME: "+attachment.MIMEType)
	}
	if attachment.UTI != "" {
		lines = append(lines, "UTI: "+attachment.UTI)
	}
	if attachment.ByteSize > 0 {
		lines = append(lines, fmt.Sprintf("Bytes: %d", attachment.ByteSize))
	}
	if attachment.ExtractStatus != "" {
		lines = append(lines, "Extract: "+attachment.ExtractStatus)
	}
	if attachment.ExtractTool != "" {
		lines = append(lines, "Extract Tool: "+attachment.ExtractTool)
	}
	if attachment.BlockedReason != "" {
		lines = append(lines, "Blocked: "+attachment.BlockedReason)
	}
	if attachment.Text != "" {
		lines = append(lines, "Text:\n"+attachment.Text)
	}
	return strings.Join(lines, "\n")
}

func countAttachments(stats *Stats, attachments []Attachment) {
	stats.AttachmentsSeen += len(attachments)
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.BlockedReason) != "" {
			stats.AttachmentsBlocked++
		}
		if attachment.ExtractStatus == "ok" {
			stats.AttachmentsExtracted++
			if attachment.ExtractTool == attachmentOCRTool {
				stats.AttachmentsOCRed++
			}
		}
		if attachment.Text != "" || attachment.URL != "" || attachment.FileName != "" || attachment.Name != "" {
			stats.AttachmentsIndexed++
		}
	}
}

func totalAttachmentTextChars(doc NoteDocument) int {
	total := 0
	for _, text := range doc.AttachmentTexts {
		total += len(text)
	}
	for _, attachment := range doc.Attachments {
		total += len(attachment.Text)
	}
	return total
}

func exclusionReason(doc NoteDocument, opts Options) string {
	if doc.Shared && opts.ExcludeShared {
		return "shared"
	}
	if doc.PasswordProtected && !opts.IncludeLocked {
		return "locked"
	}
	if matchesAny(doc.AccountName, opts.ExcludeAccounts) {
		return "account"
	}
	if matchesAny(doc.FolderPath, opts.ExcludeFolders) {
		return "folder"
	}
	return ""
}

func countSkip(stats *Stats, reason string) {
	stats.NotesSkipped++
	switch reason {
	case "account":
		stats.ExcludedAccounts++
	case "folder":
		stats.ExcludedFolders++
	case "shared":
		stats.ExcludedShared++
	case "locked":
		stats.NotesBlocked++
		stats.BlockedLocked++
	case "empty_decoded":
		stats.NotesBlocked++
		stats.BlockedEmpty++
	default:
		stats.NotesBlocked++
	}
}

func matchesAny(value string, patterns []string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	valueLower := strings.ToLower(value)
	leafLower := strings.ToLower(path.Base(value))
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		patternLower := strings.ToLower(pattern)
		if valueLower == patternLower || leafLower == patternLower {
			return true
		}
		if ok, _ := path.Match(patternLower, valueLower); ok {
			return true
		}
	}
	return false
}

func containsIgnoreMarker(value string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower("[[dbrain-ignore]]"))
}

func noteSlug(title string, externalID string) string {
	base := strings.TrimSpace(title)
	if base == "" {
		base = externalID
	}
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(base) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "note"
	}
	sum := sha256.Sum256([]byte(externalID + "\x00" + title))
	return slug + "-" + hex.EncodeToString(sum[:])[:12]
}
