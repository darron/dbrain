package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/categoryvocab"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/summaryconfig"
)

func newCategorizeVocabCommand(root *rootOptions) *cobra.Command {
	var model string
	var limit int
	var minCount int
	var timeout time.Duration
	var apply bool
	var repair bool
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "vocab",
		Short: "Ask a local LLM for categories.yaml cleanup suggestions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			opts := categorizeVocabOptions{
				Model:    model,
				Limit:    limit,
				MinCount: minCount,
				Timeout:  timeout,
				Apply:    apply,
				Repair:   repair,
				JSON:     jsonOut,
			}
			return runCategorizeVocab(cmd.Context(), cfg, opts, cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVar(&model, "model", "", "Local model to use (ollama/*; defaults to local summary/categorize model if configured)")
	cmd.Flags().IntVar(&limit, "limit", 350, "Maximum high-frequency tokens to send to the local LLM")
	cmd.Flags().IntVar(&minCount, "min-count", 2, "Only consider tokens appearing this many times or more")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Local LLM request timeout")
	cmd.Flags().BoolVar(&apply, "apply", false, "Merge accepted suggestions into categories.yaml")
	cmd.Flags().BoolVar(&repair, "repair", false, "Run categorize repair after applying suggestions")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print filtered suggestions as JSON")

	return cmd
}

type categorizeVocabOptions struct {
	Model    string
	Limit    int
	MinCount int
	Timeout  time.Duration
	Apply    bool
	Repair   bool
	JSON     bool
}

type categorizeVocabSuggestion struct {
	Aliases map[string]string `json:"aliases"`
	Drop    []string          `json:"drop"`
	Notes   []string          `json:"notes,omitempty"`
}

type categorizeVocabToken struct {
	Token string `json:"token"`
	Count int    `json:"count"`
}

var suggestCategorizeVocabWithLocalLLM = callCategorizeVocabOllama

func runCategorizeVocab(ctx context.Context, cfg config.Config, opts categorizeVocabOptions, out io.Writer) error {
	if opts.Limit <= 0 {
		opts.Limit = 350
	}
	if opts.MinCount <= 0 {
		opts.MinCount = 2
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Minute
	}

	vocab, err := categoryvocab.Load(cfg.CategoriesPath)
	if err != nil {
		return fmt.Errorf("load categories.yaml: %w", err)
	}

	st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	items, err := st.ListCategorizedItems(ctx)
	if err != nil {
		return err
	}
	sources, err := st.ListCategorizedSources(ctx)
	if err != nil {
		return err
	}
	counts := categorizeAnalyzeTokenCounts(items, sources)
	tokens := categorizeVocabTopTokens(counts, vocab, opts.MinCount, opts.Limit)
	if len(tokens) == 0 {
		_, _ = fmt.Fprintln(out, "No unmapped category/tag tokens found for the selected threshold.")
		return nil
	}

	deterministic := suggestDeterministicCategorizeVocab(counts, vocab, opts.MinCount)
	modelName, ollamaModel, err := resolveCategorizeVocabModel(cfg, opts.Model)
	if err != nil {
		return err
	}
	ollamaBase := strings.TrimRight(firstNonEmpty(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_OLLAMA_BASE_URL", "OLLAMA_BASE_URL", "OLLAMA_HOST"), "http://127.0.0.1:11434"), "/")
	ollamaKey := firstNonEmpty(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_OLLAMA_API_KEY", "OLLAMA_API_KEY"), "ollama")

	llmSuggestion, err := suggestCategorizeVocabWithLocalLLM(ctx, categorizeVocabLLMRequest{
		Model:      modelName,
		OllamaName: ollamaModel,
		OllamaBase: ollamaBase,
		OllamaKey:  ollamaKey,
		Timeout:    opts.Timeout,
		Tokens:     tokens,
	})
	if err != nil && len(deterministic.Aliases) == 0 && len(deterministic.Drop) == 0 {
		return err
	}
	if err != nil {
		deterministic.Notes = append(deterministic.Notes, "Local LLM suggestion failed: "+err.Error())
	}
	llmSuggestion = filterCategorizeVocabSuggestion(llmSuggestion, vocab, tokens)
	suggestion := mergeCategorizeVocabSuggestions(deterministic, llmSuggestion)
	suggestion = normalizeCategorizeVocabSuggestionForOutput(suggestion)

	if opts.JSON {
		if err := writeJSON(out, suggestion); err != nil {
			return err
		}
	} else {
		printCategorizeVocabSuggestion(out, suggestion, modelName, len(tokens), opts.MinCount, opts.Apply)
	}

	if !opts.Apply {
		if opts.Repair {
			_, _ = fmt.Fprintln(out, "\n--repair ignored because --apply was not set.")
		}
		return nil
	}

	mergeStats, err := mergeCategorizeVocabSuggestionFile(cfg.CategoriesPath, suggestion, time.Now())
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintf(out, "categories.yaml updated: aliases=%d drop=%d\n", mergeStats.AliasesAdded, mergeStats.DropAdded)
	if opts.Repair {
		_, _ = fmt.Fprintln(out, "\nRepairing existing user_tags with updated vocabulary...")
		_, err := runCategorizeVocabRepair(ctx, cfg.DBPath, cfg.CacheDir, cfg.CategoriesPath, false, out)
		return err
	}
	return nil
}

func normalizeCategorizeVocabSuggestionForOutput(s categorizeVocabSuggestion) categorizeVocabSuggestion {
	if s.Aliases == nil {
		s.Aliases = map[string]string{}
	}
	if s.Drop == nil {
		s.Drop = []string{}
	}
	return s
}

func mergeCategorizeVocabSuggestions(values ...categorizeVocabSuggestion) categorizeVocabSuggestion {
	out := categorizeVocabSuggestion{Aliases: map[string]string{}}
	dropSeen := make(map[string]struct{})
	for _, value := range values {
		for from, to := range value.Aliases {
			from = categoryvocab.Normalize(from)
			to = categoryvocab.Normalize(to)
			if from != "" && to != "" && from != to {
				out.Aliases[from] = to
			}
		}
		for _, raw := range value.Drop {
			token := categoryvocab.Normalize(raw)
			if token == "" {
				continue
			}
			if _, ok := dropSeen[token]; ok {
				continue
			}
			dropSeen[token] = struct{}{}
			out.Drop = append(out.Drop, token)
		}
		out.Notes = append(out.Notes, value.Notes...)
	}
	sort.Strings(out.Drop)
	if len(out.Aliases) == 0 {
		out.Aliases = nil
	}
	if len(out.Aliases) == 0 && len(out.Drop) == 0 {
		out.Notes = nil
	}
	return out
}

func categorizeVocabTopTokens(counts map[string]int, vocab categoryvocab.Vocab, minCount, limit int) []categorizeVocabToken {
	tokens := make([]categorizeVocabToken, 0, len(counts))
	for token, count := range counts {
		if count < minCount {
			continue
		}
		normalized := categoryvocab.Normalize(token)
		applied := vocab.ApplyToTokens([]string{normalized})
		if len(applied) == 0 || applied[0] != normalized {
			continue
		}
		tokens = append(tokens, categorizeVocabToken{Token: normalized, Count: count})
	}
	sort.Slice(tokens, func(i, j int) bool {
		if tokens[i].Count != tokens[j].Count {
			return tokens[i].Count > tokens[j].Count
		}
		return tokens[i].Token < tokens[j].Token
	})
	if limit > 0 && len(tokens) > limit {
		tokens = tokens[:limit]
	}
	return tokens
}

func suggestDeterministicCategorizeVocab(counts map[string]int, vocab categoryvocab.Vocab, minCount int) categorizeVocabSuggestion {
	tokenExists := func(token string) bool {
		token = categoryvocab.Normalize(token)
		if counts[token] < minCount {
			return false
		}
		applied := vocab.ApplyToTokens([]string{token})
		return len(applied) > 0 && applied[0] == token
	}
	canonicalExists := func(token string) bool {
		token = categoryvocab.Normalize(token)
		if counts[token] >= minCount {
			return true
		}
		applied := vocab.ApplyToTokens([]string{token})
		return len(applied) > 0 && applied[0] == token
	}

	aliases := make(map[string]string)
	for from, to := range knownCategorizeVocabAliases {
		if tokenExists(from) && canonicalExists(to) {
			aliases[from] = to
		}
	}

	dropSeen := make(map[string]struct{})
	var drop []string
	for _, token := range knownCategorizeVocabDrops {
		if !tokenExists(token) {
			continue
		}
		if _, ok := dropSeen[token]; ok {
			continue
		}
		dropSeen[token] = struct{}{}
		drop = append(drop, token)
	}
	sort.Strings(drop)
	if len(aliases) == 0 {
		aliases = nil
	}
	return categorizeVocabSuggestion{Aliases: aliases, Drop: drop}
}

var knownCategorizeVocabAliases = map[string]string{
	"amazon-eks":               "aws-eks",
	"eks":                      "aws-eks",
	"go-libraries":             "go-library",
	"golang-library":           "go-library",
	"google-kubernetes-engine": "gke",
	"hashicorp-terraform":      "terraform",
	"k8s":                      "kubernetes",
	"kubernetes-utilities":     "kubernetes-tools",
	"llm":                      "large-language-models",
	"llms":                     "large-language-models",
	"local-llms":               "local-llm",
	"mtls":                     "mutual-tls",
	"prometheus-exporter":      "prometheus",
	"prometheus-metrics":       "prometheus",
	"python-libraries":         "python-library",
	"javascript-libraries":     "javascript-library",
	"sre":                      "site-reliability-engineering",
	"ssl-certificates":         "tls-certificates",
	"ssl-tls":                  "tls",
	"ssl-tls-certificates":     "tls-certificates",
	"twitter-x":                "twitter",
	"x-twitter":                "twitter",
}

var knownCategorizeVocabDrops = []string{
	"article-link",
	"blog-post",
	"code-repositories",
	"code-repository",
	"external-link",
	"external-links",
	"git-repositories",
	"git-repository",
	"github-repo",
	"github-repositories",
	"repositories",
	"repository",
	"social-media-post",
	"social-media-posts",
	"social-post",
	"software-repository",
	"source-code-repository",
	"tweet",
	"tweets",
	"twitter-article",
	"twitter-articles",
	"twitter-link",
	"twitter-quote",
	"user-content",
	"unknown-content",
	"x-post",
}

func resolveCategorizeVocabModel(cfg config.Config, explicit string) (string, string, error) {
	candidates := []string{
		strings.TrimSpace(explicit),
		summaryconfig.Model(cfg.RootDir, ""),
		runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_CATEGORIZE_MODEL"),
	}
	for _, candidate := range candidates {
		if model, ok := parseLocalOllamaModel(candidate); ok {
			return "ollama/" + model, model, nil
		}
	}
	return "", "", fmt.Errorf("categorize vocab requires a local Ollama model; pass --model ollama/<model> or set DBRAIN_SUMMARY_MODEL/DBRAIN_CATEGORIZE_MODEL to ollama/<model>")
}

func parseLocalOllamaModel(model string) (string, bool) {
	value := strings.TrimSpace(model)
	lower := strings.ToLower(value)
	switch {
	case strings.HasPrefix(lower, "ollama/"):
		return strings.TrimSpace(value[7:]), strings.TrimSpace(value[7:]) != ""
	case strings.HasPrefix(lower, "ollama:"):
		return strings.TrimSpace(value[7:]), strings.TrimSpace(value[7:]) != ""
	default:
		return "", false
	}
}

type categorizeVocabLLMRequest struct {
	Model      string
	OllamaName string
	OllamaBase string
	OllamaKey  string
	Timeout    time.Duration
	Tokens     []categorizeVocabToken
}

func callCategorizeVocabOllama(ctx context.Context, req categorizeVocabLLMRequest) (categorizeVocabSuggestion, error) {
	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	think := false
	body := map[string]any{
		"model":  req.OllamaName,
		"stream": false,
		"think":  &think,
		"messages": []message{
			{Role: "system", Content: categorizeVocabSystemPrompt},
			{Role: "user", Content: categorizeVocabUserPrompt(req.Tokens)},
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return categorizeVocabSuggestion{}, fmt.Errorf("marshal ollama request: %w", err)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(timeoutCtx, http.MethodPost, strings.TrimRight(req.OllamaBase, "/")+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return categorizeVocabSuggestion{}, fmt.Errorf("create ollama request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(req.OllamaKey) != "" {
		httpReq.Header.Set("Authorization", "Bearer "+req.OllamaKey)
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return categorizeVocabSuggestion{}, fmt.Errorf("ollama vocab suggestion: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return categorizeVocabSuggestion{}, fmt.Errorf("read ollama response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		preview := strings.TrimSpace(string(raw))
		if len(preview) > 300 {
			preview = preview[:300]
		}
		return categorizeVocabSuggestion{}, fmt.Errorf("ollama vocab suggestion: http %d: %s", resp.StatusCode, preview)
	}

	var parsed struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return categorizeVocabSuggestion{}, fmt.Errorf("parse ollama response: %w", err)
	}
	return parseCategorizeVocabSuggestion(parsed.Message.Content)
}

const categorizeVocabSystemPrompt = `You propose cleanup rules for categories.yaml in a local personal knowledge base.
You will receive high-frequency tag/category tokens with counts.
Return ONLY valid JSON. No markdown. No comments.

Required JSON:
{
  "aliases": {"non-canonical-token": "canonical-token"},
  "drop": ["low-signal-token"],
  "notes": ["short optional note"]
}

Rules:
- Be conservative. Prefer no suggestion over a risky suggestion.
- Only suggest aliases where the meaning is the same: spelling variants, singular/plural variants, abbreviations, product renames, or obvious typos.
- Do not collapse related but distinct concepts. For example, "kubernetes", "helm", and "service-mesh" are different.
- Do not drop topical tokens, proper nouns, named projects, people, places, companies, or technical concepts.
- Drop only import/source noise or content-free generic labels.
- All tokens must be lowercase-hyphenated.`

func categorizeVocabUserPrompt(tokens []categorizeVocabToken) string {
	var sb strings.Builder
	sb.WriteString("Candidate tokens:\n")
	for _, token := range tokens {
		_, _ = fmt.Fprintf(&sb, "- %s: %d\n", token.Token, token.Count)
	}
	return sb.String()
}

func parseCategorizeVocabSuggestion(content string) (categorizeVocabSuggestion, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	if start := strings.Index(content, "{"); start >= 0 {
		if end := strings.LastIndex(content, "}"); end > start {
			content = content[start : end+1]
		}
	}
	var suggestion categorizeVocabSuggestion
	if err := json.Unmarshal([]byte(content), &suggestion); err != nil {
		return categorizeVocabSuggestion{}, fmt.Errorf("parse vocab suggestion JSON: %w", err)
	}
	return suggestion, nil
}

func filterCategorizeVocabSuggestion(s categorizeVocabSuggestion, vocab categoryvocab.Vocab, tokens []categorizeVocabToken) categorizeVocabSuggestion {
	tokenSet := make(map[string]struct{}, len(tokens))
	tokenCounts := make(map[string]int, len(tokens))
	for _, token := range tokens {
		tokenSet[token.Token] = struct{}{}
		tokenCounts[token.Token] = token.Count
	}
	canonicalSet := make(map[string]struct{}, len(tokenSet)+len(vocab.Aliases))
	for token := range tokenSet {
		canonicalSet[token] = struct{}{}
	}
	for _, to := range vocab.Aliases {
		if normalized := categoryvocab.Normalize(to); normalized != "" {
			canonicalSet[normalized] = struct{}{}
		}
	}

	aliases := make(map[string]string, len(s.Aliases))
	for from, to := range s.Aliases {
		from = categoryvocab.Normalize(from)
		to = categoryvocab.Normalize(to)
		if from == "" || to == "" || from == to {
			continue
		}
		if _, ok := tokenSet[from]; !ok {
			continue
		}
		if applied := vocab.ApplyToTokens([]string{from}); len(applied) == 0 || applied[0] != from {
			continue
		}
		if applied := vocab.ApplyToTokens([]string{to}); len(applied) > 0 {
			to = applied[0]
		}
		if _, ok := canonicalSet[to]; !ok {
			continue
		}
		if !safeCategorizeVocabAlias(from, to) {
			continue
		}
		if tokenCounts[to] > 0 && tokenCounts[to] < tokenCounts[from] {
			continue
		}
		aliases[from] = to
	}

	dropSeen := make(map[string]struct{})
	drop := make([]string, 0, len(s.Drop))
	for _, token := range s.Drop {
		token = categoryvocab.Normalize(token)
		if token == "" {
			continue
		}
		if _, ok := tokenSet[token]; !ok {
			continue
		}
		if applied := vocab.ApplyToTokens([]string{token}); len(applied) == 0 || applied[0] != token {
			continue
		}
		if !safeCategorizeVocabDrop(token) {
			continue
		}
		if _, ok := dropSeen[token]; ok {
			continue
		}
		dropSeen[token] = struct{}{}
		drop = append(drop, token)
	}
	sort.Strings(drop)
	return categorizeVocabSuggestion{Aliases: aliases, Drop: drop}
}

func safeCategorizeVocabAlias(from, to string) bool {
	if from == "" || to == "" || from == to {
		return false
	}
	if knownTo, ok := knownCategorizeVocabAliases[from]; ok && knownTo == to {
		return true
	}
	if singularEquivalent(from, to) {
		return true
	}
	if editDistanceWithin(from, to, 2) && minInt(len(from), len(to)) >= 6 {
		return true
	}
	return false
}

func singularEquivalent(a, b string) bool {
	return singularForm(a) == singularForm(b)
}

func singularForm(token string) string {
	replacements := map[string]string{
		"-tools":     "-tool",
		"-libraries": "-library",
		"-projects":  "-project",
		"-models":    "-model",
		"-agents":    "-agent",
		"-apps":      "-app",
		"-utilities": "-utility",
	}
	for suffix, replacement := range replacements {
		if strings.HasSuffix(token, suffix) {
			return strings.TrimSuffix(token, suffix) + replacement
		}
	}
	if strings.HasSuffix(token, "ies") && len(token) > 4 {
		return strings.TrimSuffix(token, "ies") + "y"
	}
	if strings.HasSuffix(token, "s") && !strings.HasSuffix(token, "ss") && len(token) > 3 {
		return strings.TrimSuffix(token, "s")
	}
	return token
}

func safeCategorizeVocabDrop(token string) bool {
	if token == "" {
		return false
	}
	switch token {
	case "bookmark", "bookmarks", "links", "source", "sources", "import", "imports", "unknown", "generic", "user-content", "unknown-content":
		return true
	}
	for _, known := range knownCategorizeVocabDrops {
		if token == known {
			return true
		}
	}
	return strings.HasSuffix(token, "-link") ||
		strings.HasSuffix(token, "-links") ||
		strings.Contains(token, "external-link") ||
		strings.Contains(token, "social-media-link") ||
		strings.Contains(token, "user-content") ||
		strings.Contains(token, "unknown-content")
}

func editDistanceWithin(a, b string, maxDistance int) bool {
	if absInt(len(a)-len(b)) > maxDistance {
		return false
	}
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		rowMin := cur[0]
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			cur[j] = minInt(minInt(cur[j-1]+1, prev[j]+1), prev[j-1]+cost)
			if cur[j] < rowMin {
				rowMin = cur[j]
			}
		}
		if rowMin > maxDistance {
			return false
		}
		prev = cur
	}
	return prev[len(b)] <= maxDistance
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func printCategorizeVocabSuggestion(out io.Writer, s categorizeVocabSuggestion, model string, tokenCount, minCount int, applying bool) {
	_, _ = fmt.Fprintf(out, "Model: %s\n", model)
	_, _ = fmt.Fprintf(out, "Analyzed tokens: %d (min-count=%d)\n\n", tokenCount, minCount)
	if len(s.Aliases) == 0 && len(s.Drop) == 0 {
		_, _ = fmt.Fprintln(out, "No conservative vocabulary suggestions returned.")
		return
	}
	_, _ = fmt.Fprintln(out, "Suggested categories.yaml patch:")
	if len(s.Aliases) > 0 {
		_, _ = fmt.Fprintln(out, "\naliases:")
		keys := make([]string, 0, len(s.Aliases))
		for key := range s.Aliases {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			_, _ = fmt.Fprintf(out, "  %s: %s\n", key, s.Aliases[key])
		}
	}
	if len(s.Drop) > 0 {
		_, _ = fmt.Fprintln(out, "\ndrop:")
		for _, token := range s.Drop {
			_, _ = fmt.Fprintf(out, "  - %s\n", token)
		}
	}
	if len(s.Notes) > 0 {
		_, _ = fmt.Fprintln(out, "\nNotes:")
		for _, note := range s.Notes {
			_, _ = fmt.Fprintf(out, "- %s\n", note)
		}
	}
	if applying {
		_, _ = fmt.Fprintln(out, "\nApplying accepted suggestions to categories.yaml.")
	} else {
		_, _ = fmt.Fprintln(out, "\nRun again with --apply to merge these rules into categories.yaml, and --repair to rewrite existing user_tags.")
	}
}

type categorizeVocabMergeStats struct {
	AliasesAdded int
	DropAdded    int
}

func mergeCategorizeVocabSuggestionFile(path string, s categorizeVocabSuggestion, now time.Time) (categorizeVocabMergeStats, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return categorizeVocabMergeStats{}, fmt.Errorf("read categories.yaml: %w", err)
	}
	merged, stats, err := mergeCategorizeVocabSuggestion(data, s, now)
	if err != nil {
		return categorizeVocabMergeStats{}, err
	}
	if err := os.WriteFile(path, merged, 0o644); err != nil {
		return categorizeVocabMergeStats{}, fmt.Errorf("write categories.yaml: %w", err)
	}
	return stats, nil
}

func mergeCategorizeVocabSuggestion(data []byte, s categorizeVocabSuggestion, now time.Time) ([]byte, categorizeVocabMergeStats, error) {
	var existing categoryvocab.Vocab
	if err := yamlUnmarshalCategoryVocab(data, &existing); err != nil {
		return nil, categorizeVocabMergeStats{}, err
	}

	filtered := dedupeCategorizeVocabSuggestion(s, existing)
	stats := categorizeVocabMergeStats{AliasesAdded: len(filtered.Aliases), DropAdded: len(filtered.Drop)}
	if stats.AliasesAdded == 0 && stats.DropAdded == 0 {
		return data, stats, nil
	}

	text := string(data)
	stamp := now.Format("2006-01-02")
	if len(filtered.Aliases) > 0 {
		block := renderAliasBlock(filtered.Aliases, stamp)
		idx := strings.Index(text, "\ndrop:\n")
		if idx < 0 {
			return nil, stats, fmt.Errorf("categories.yaml missing top-level drop: section")
		}
		text = strings.TrimRight(text[:idx], "\n") + "\n\n" + block + "\n" + text[idx:]
	}
	if len(filtered.Drop) > 0 {
		block := renderDropBlock(filtered.Drop, stamp)
		text = strings.TrimRight(text, "\n") + "\n\n" + block + "\n"
	}
	return []byte(text), stats, nil
}

func dedupeCategorizeVocabSuggestion(s categorizeVocabSuggestion, existing categoryvocab.Vocab) categorizeVocabSuggestion {
	aliases := make(map[string]string, len(s.Aliases))
	for from, to := range s.Aliases {
		from = categoryvocab.Normalize(from)
		to = categoryvocab.Normalize(to)
		if from == "" || to == "" || from == to {
			continue
		}
		applied := existing.ApplyToTokens([]string{from})
		if len(applied) == 0 || applied[0] != from {
			continue
		}
		aliases[from] = to
	}

	dropSeen := make(map[string]struct{})
	drop := make([]string, 0, len(s.Drop))
	for _, raw := range s.Drop {
		token := categoryvocab.Normalize(raw)
		if token == "" {
			continue
		}
		applied := existing.ApplyToTokens([]string{token})
		if len(applied) == 0 || applied[0] != token {
			continue
		}
		if _, ok := dropSeen[token]; ok {
			continue
		}
		dropSeen[token] = struct{}{}
		drop = append(drop, token)
	}
	sort.Strings(drop)
	if len(aliases) == 0 {
		aliases = nil
	}
	return categorizeVocabSuggestion{Aliases: aliases, Drop: drop}
}

func yamlUnmarshalCategoryVocab(data []byte, out *categoryvocab.Vocab) error {
	vocab, err := categoryvocab.Parse(data)
	if err != nil {
		return fmt.Errorf("parse categories.yaml: %w", err)
	}
	*out = vocab
	return nil
}

func renderAliasBlock(aliases map[string]string, stamp string) string {
	keys := make([]string, 0, len(aliases))
	for key := range aliases {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "  # === LLM-suggested cleanup (%s) ===\n", stamp)
	for _, key := range keys {
		_, _ = fmt.Fprintf(&sb, "  %s: %s\n", key, aliases[key])
	}
	return strings.TrimRight(sb.String(), "\n")
}

func renderDropBlock(drop []string, stamp string) string {
	drop = append([]string(nil), drop...)
	sort.Strings(drop)
	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "  # LLM-suggested cleanup (%s)\n", stamp)
	for _, token := range drop {
		_, _ = fmt.Fprintf(&sb, "  - %s\n", token)
	}
	return strings.TrimRight(sb.String(), "\n")
}
