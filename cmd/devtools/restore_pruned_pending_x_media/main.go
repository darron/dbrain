package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/mediadownload"
	"github.com/darron/dbrain/internal/store"
)

func main() {
	var root string
	var limit int
	var includeOCR bool
	var includeTranscripts bool
	var timeout time.Duration

	flag.StringVar(&root, "root", ".", "brain root")
	flag.IntVar(&limit, "limit", 5000, "maximum pending items to restore per category")
	flag.BoolVar(&includeOCR, "ocr", true, "restore pending pruned OCR items")
	flag.BoolVar(&includeTranscripts, "transcripts", true, "restore pending pruned transcript items")
	flag.DurationVar(&timeout, "timeout", 45*time.Second, "per-media download timeout")
	flag.Parse()

	cfg, err := config.Load(root)
	if err != nil {
		die(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		die(err)
	}

	rawDB, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		die(fmt.Errorf("open sqlite db: %w", err))
	}
	defer func() {
		_ = rawDB.Close()
	}()

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		die(err)
	}
	defer func() {
		_ = st.Close()
	}()

	itemIDs := map[int64]struct{}{}
	if includeTranscripts {
		ids, err := loadIDs(rawDB, pendingTranscriptQuery, limit)
		if err != nil {
			die(err)
		}
		for _, id := range ids {
			itemIDs[id] = struct{}{}
		}
		fmt.Printf("Pending pruned transcript items: %d\n", len(ids))
	}
	if includeOCR {
		ids, err := loadIDs(rawDB, pendingOCRQuery, limit)
		if err != nil {
			die(err)
		}
		for _, id := range ids {
			itemIDs[id] = struct{}{}
		}
		fmt.Printf("Pending pruned OCR items: %d\n", len(ids))
	}

	if len(itemIDs) == 0 {
		fmt.Println("Nothing to restore.")
		return
	}

	ordered := make([]int64, 0, len(itemIDs))
	for id := range itemIDs {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	itemsRestored := 0
	totalCandidates := 0
	totalRequested := 0
	totalDownloaded := 0
	totalGone := 0
	totalErrors := 0
	totalChanged := 0

	for _, itemID := range ordered {
		stats, err := mediadownload.RunForItem(ctx, cfg, st, itemID, mediadownload.Options{
			Force:   true,
			Timeout: timeout,
			Logger:  logger,
		})
		if err != nil {
			die(fmt.Errorf("restore item %d: %w", itemID, err))
		}
		if stats.Requested > 0 || stats.Changed > 0 {
			itemsRestored++
		}
		totalCandidates += stats.Candidates
		totalRequested += stats.Requested
		totalDownloaded += stats.Downloaded
		totalGone += stats.Gone
		totalErrors += stats.Errors
		totalChanged += stats.Changed
	}

	fmt.Printf("Items visited: %d\n", len(ordered))
	fmt.Printf("Items restored: %d\n", itemsRestored)
	fmt.Printf("Media candidates: %d\n", totalCandidates)
	fmt.Printf("Media requested: %d\n", totalRequested)
	fmt.Printf("Media downloaded: %d\n", totalDownloaded)
	fmt.Printf("Media gone: %d\n", totalGone)
	fmt.Printf("Media errors: %d\n", totalErrors)
	fmt.Printf("Media changed: %d\n", totalChanged)
}

func die(err error) {
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		msg = "unknown error"
	}
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
