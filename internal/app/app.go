package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dbrain/internal/config"
	"dbrain/internal/ftimport"
	"dbrain/internal/linkextract"
	"dbrain/internal/store"
	"dbrain/internal/xapi"
)

func Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		printUsage(os.Stdout)
		return nil
	}

	switch args[0] {
	case "import":
		return runImport(ctx, args[1:])
	case "extract":
		return runExtract(ctx, args[1:])
	case "hydrate":
		return runHydrate(ctx, args[1:])
	case "search":
		return runSearch(ctx, args[1:])
	case "get":
		return runGet(ctx, args[1:])
	case "help", "-h", "--help":
		printUsage(os.Stdout)
		return nil
	default:
		printUsage(os.Stderr)
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func runExtract(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "links" {
		return fmt.Errorf("usage: dbrain extract links [flags]")
	}

	fs := flag.NewFlagSet("extract links", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)

	root := fs.String("root", ".", "Brain root directory")
	discoverLimit := fs.Int("discover-limit", 500, "Maximum bookmark items to scan for outbound links")
	limit := fs.Int("limit", 50, "Maximum deduped sources to enrich")
	force := fs.Bool("force", false, "Reprocess items and sources even if they were already discovered or enriched")
	summarize := fs.Bool("summarize", true, "Run summarize.sh summarization after extraction")
	model := fs.String("model", "", "Optional summarize model override")
	cliProvider := fs.String("cli", "", "Optional summarize CLI provider override")
	length := fs.String("length", "medium", "Summary length for summarize.sh")
	timeout := fs.Duration("timeout", 2*time.Minute, "Timeout for summarize.sh extraction and summarization")
	debug := fs.Bool("debug", false, "Enable structured debug logging to stderr")
	jsonOut := fs.Bool("json", false, "Print extraction stats as JSON")

	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	cfg, err := config.Load(*root)
	if err != nil {
		return err
	}
	if err := cfg.EnsureDirs(); err != nil {
		return err
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = st.Close()
	}()

	var logger *slog.Logger
	if *debug {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}

	stats, err := linkextract.Run(ctx, cfg, st, linkextract.Options{
		DiscoverLimit: *discoverLimit,
		Limit:         *limit,
		Force:         *force,
		Summarize:     *summarize,
		Model:         *model,
		CLI:           *cliProvider,
		Length:        *length,
		Timeout:       *timeout,
		Logger:        logger,
	})
	if err != nil {
		return err
	}

	if *jsonOut {
		return writeJSON(os.Stdout, stats)
	}

	_, _ = fmt.Fprintf(os.Stdout, "Items scanned: %d\n", stats.ItemsScanned)
	_, _ = fmt.Fprintf(os.Stdout, "Items marked: %d\n", stats.ItemsMarked)
	_, _ = fmt.Fprintf(os.Stdout, "Links found: %d\n", stats.LinksFound)
	_, _ = fmt.Fprintf(os.Stdout, "Sources created: %d\n", stats.SourcesCreated)
	_, _ = fmt.Fprintf(os.Stdout, "Links created: %d\n", stats.LinksCreated)
	_, _ = fmt.Fprintf(os.Stdout, "Sources queued: %d\n", stats.SourcesQueued)
	_, _ = fmt.Fprintf(os.Stdout, "Sources extracted: %d\n", stats.SourcesExtracted)
	_, _ = fmt.Fprintf(os.Stdout, "Sources summarized: %d\n", stats.SourcesSummarized)
	_, _ = fmt.Fprintf(os.Stdout, "Sources rendered: %d\n", stats.SourcesRendered)
	_, _ = fmt.Fprintf(os.Stdout, "Source unchanged writes: %d\n", stats.SourcesUnchanged)
	_, _ = fmt.Fprintf(os.Stdout, "Errors: %d\n", stats.Errors)

	return nil
}

func runImport(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "ft" {
		return fmt.Errorf("usage: dbrain import ft [flags]")
	}

	home, _ := os.UserHomeDir()
	fs := flag.NewFlagSet("import ft", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)

	root := fs.String("root", ".", "Brain root directory")
	source := fs.String("source", filepath.Join(home, ".ft-bookmarks", "bookmarks.db"), "Path to ft bookmarks.db")
	limit := fs.Int("limit", 0, "Limit imported rows for smoke testing")
	jsonOut := fs.Bool("json", false, "Print import stats as JSON")

	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	cfg, err := config.Load(*root)
	if err != nil {
		return err
	}
	if err := cfg.EnsureDirs(); err != nil {
		return err
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = st.Close()
	}()

	stats, err := ftimport.Run(ctx, cfg, st, ftimport.Options{
		SourcePath: *source,
		Limit:      *limit,
	})
	if err != nil {
		return err
	}

	if *jsonOut {
		return writeJSON(os.Stdout, stats)
	}

	_, _ = fmt.Fprintf(os.Stdout, "Imported %d bookmarks\n", stats.Processed)
	_, _ = fmt.Fprintf(os.Stdout, "Created: %d\n", stats.Created)
	_, _ = fmt.Fprintf(os.Stdout, "Updated: %d\n", stats.Updated)
	_, _ = fmt.Fprintf(os.Stdout, "Unchanged: %d\n", stats.Unchanged)
	_, _ = fmt.Fprintf(os.Stdout, "Rendered notes: %d\n", stats.Rendered)
	_, _ = fmt.Fprintf(os.Stdout, "Brain DB: %s\n", cfg.DBPath)
	_, _ = fmt.Fprintf(os.Stdout, "Vault: %s\n", cfg.VaultDir)

	return nil
}

func runHydrate(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "x" {
		return fmt.Errorf("usage: dbrain hydrate x [flags]")
	}

	fs := flag.NewFlagSet("hydrate x", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)

	root := fs.String("root", ".", "Brain root directory")
	limit := fs.Int("limit", 100, "Maximum items to hydrate")
	concurrency := fs.Int("concurrency", 4, "Number of concurrent post fetches")
	force := fs.Bool("force", false, "Refetch items even if they already have X API hydration")
	browser := fs.String("browser", "", "Preferred browser for cookie lookup (chrome, brave, chromium, edge, firefox, safari)")
	profile := fs.String("profile", "", "Browser profile override; requires --browser")
	ct0 := fs.String("ct0", "", "Manual ct0 cookie override")
	authToken := fs.String("auth-token", "", "Manual auth_token cookie override")
	timeout := fs.Duration("timeout", 30*time.Second, "Timeout for browser helpers and X HTTP requests")
	debug := fs.Bool("debug", false, "Enable structured debug logging to stderr")
	jsonOut := fs.Bool("json", false, "Print hydration stats as JSON")

	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	cfg, err := config.Load(*root)
	if err != nil {
		return err
	}
	if err := cfg.EnsureDirs(); err != nil {
		return err
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = st.Close()
	}()

	var logger *slog.Logger
	if *debug {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}

	stats, err := xapi.Run(ctx, cfg, st, xapi.Options{
		Limit:       *limit,
		Force:       *force,
		Concurrency: *concurrency,
		Browser:     *browser,
		Profile:     *profile,
		CT0:         *ct0,
		AuthToken:   *authToken,
		Timeout:     *timeout,
		Logger:      logger,
	})
	if err != nil {
		return err
	}

	if *jsonOut {
		return writeJSON(os.Stdout, stats)
	}

	_, _ = fmt.Fprintf(os.Stdout, "Hydration candidates: %d\n", stats.Candidates)
	_, _ = fmt.Fprintf(os.Stdout, "Requested: %d\n", stats.Requested)
	_, _ = fmt.Fprintf(os.Stdout, "Hydrated: %d\n", stats.Hydrated)
	_, _ = fmt.Fprintf(os.Stdout, "Missing: %d\n", stats.Missing)
	_, _ = fmt.Fprintf(os.Stdout, "API errors: %d\n", stats.APIErrors)
	_, _ = fmt.Fprintf(os.Stdout, "Rendered notes: %d\n", stats.Rendered)
	_, _ = fmt.Fprintf(os.Stdout, "Unchanged: %d\n", stats.Unchanged)

	return nil
}

func runSearch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)

	root := fs.String("root", ".", "Brain root directory")
	limit := fs.Int("limit", 10, "Maximum results")
	jsonOut := fs.Bool("json", false, "Print search results as JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: dbrain search [flags] <query>")
	}

	query := strings.Join(fs.Args(), " ")
	cfg, err := config.Load(*root)
	if err != nil {
		return err
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = st.Close()
	}()

	results, err := st.Search(ctx, query, *limit)
	if err != nil {
		return err
	}

	if *jsonOut {
		return writeJSON(os.Stdout, results)
	}

	if len(results) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "No results.")
		return nil
	}

	for _, result := range results {
		_, _ = fmt.Fprintln(os.Stdout, result.SourceKey)
		if result.Title != "" {
			_, _ = fmt.Fprintf(os.Stdout, "  title: %s\n", result.Title)
		}
		if result.AuthorHandle != "" || result.AuthorName != "" {
			label := strings.TrimSpace(result.AuthorName)
			if result.AuthorHandle != "" {
				if label != "" {
					label += " "
				}
				label += "@" + strings.TrimSpace(result.AuthorHandle)
			}
			_, _ = fmt.Fprintf(os.Stdout, "  author: %s\n", label)
		}
		_, _ = fmt.Fprintf(os.Stdout, "  url: %s\n", result.CanonicalURL)
		_, _ = fmt.Fprintf(os.Stdout, "  note: %s\n", filepath.Join(cfg.VaultDir, filepath.FromSlash(result.NotePath)))
		if result.Snippet != "" {
			_, _ = fmt.Fprintf(os.Stdout, "  snippet: %s\n", result.Snippet)
		}
	}

	return nil
}

func runGet(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("get", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)

	root := fs.String("root", ".", "Brain root directory")
	jsonOut := fs.Bool("json", false, "Print the item as JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: dbrain get [flags] <source-key-or-id>")
	}

	lookup := fs.Arg(0)
	cfg, err := config.Load(*root)
	if err != nil {
		return err
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = st.Close()
	}()

	item, err := st.GetItem(ctx, lookup)
	if err == nil {
		if *jsonOut {
			return writeJSON(os.Stdout, item)
		}

		fullPath := filepath.Join(cfg.VaultDir, filepath.FromSlash(item.NotePath))
		content, err := os.ReadFile(fullPath)
		if err != nil {
			return fmt.Errorf("read note %s: %w", fullPath, err)
		}
		_, _ = fmt.Fprint(os.Stdout, string(content))
		return nil
	}

	source, sourceErr := st.GetSource(ctx, lookup)
	if sourceErr != nil {
		return err
	}
	if *jsonOut {
		return writeJSON(os.Stdout, source)
	}

	fullPath := filepath.Join(cfg.VaultDir, filepath.FromSlash(source.NotePath))
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return fmt.Errorf("read note %s: %w", fullPath, err)
	}
	_, _ = fmt.Fprint(os.Stdout, string(content))
	return nil
}

func writeJSON(dst *os.File, value interface{}) error {
	encoder := json.NewEncoder(dst)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func printUsage(dst *os.File) {
	_, _ = fmt.Fprintln(dst, "Usage:")
	_, _ = fmt.Fprintln(dst, "  dbrain import ft [flags]")
	_, _ = fmt.Fprintln(dst, "  dbrain extract links [flags]")
	_, _ = fmt.Fprintln(dst, "  dbrain hydrate x [flags]")
	_, _ = fmt.Fprintln(dst, "  dbrain search [flags] <query>")
	_, _ = fmt.Fprintln(dst, "  dbrain get [flags] <source-key-or-id>")
}
