package okf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/entities"
	"github.com/darron/dbrain/internal/runlock"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/topics"
	"github.com/darron/dbrain/internal/vault"
)

func Export(ctx context.Context, cfg config.Config, st *store.Store, opts ExportOptions) (ExportResult, error) {
	if st == nil {
		return ExportResult{}, fmt.Errorf("store is required")
	}
	opts = normalizeExportOptions(opts)
	if opts.Profile != ProfilePrivate {
		return ExportResult{}, fmt.Errorf("okf profile %q is not implemented; only private is supported", opts.Profile)
	}
	target := strings.TrimSpace(opts.OutDir)
	if target == "" {
		target = cfg.OKFDir
	}
	if target == "" {
		return ExportResult{}, fmt.Errorf("okf output directory is required")
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return ExportResult{}, fmt.Errorf("resolve okf output directory: %w", err)
	}
	if err := rejectSymlink(targetAbs); err != nil {
		return ExportResult{}, err
	}
	parent := filepath.Dir(targetAbs)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return ExportResult{}, fmt.Errorf("create okf output parent %s: %w", parent, err)
	}

	unlock, err := acquireLock(parent)
	if err != nil {
		return ExportResult{}, err
	}
	defer unlock()

	snapshot, err := st.LoadOKFExportSnapshot(ctx, store.OKFExportOptions{
		IncludeItems:   opts.IncludeItems,
		IncludeSources: opts.IncludeSources,
		SourceTypes:    opts.SourceTypes,
		Limit:          opts.Limit,
	})
	if err != nil {
		return ExportResult{}, err
	}
	if opts.IncludeEntities && len(opts.Entities) == 0 {
		derived, err := entities.BuildIndex(ctx, st)
		if err != nil {
			return ExportResult{}, fmt.Errorf("build okf entities: %w", err)
		}
		opts.Entities = derived
	}
	if opts.IncludeTopics && len(opts.Topics) == 0 {
		graphs, err := exportTopicGraphs(ctx, cfg, st)
		if err != nil {
			return ExportResult{}, err
		}
		opts.Topics = graphs
	}
	bundle, err := BuildBundle(snapshot, opts)
	if err != nil {
		return ExportResult{}, err
	}

	staging, err := os.MkdirTemp(parent, ".dbrain-okf-staging-*")
	if err != nil {
		return ExportResult{}, fmt.Errorf("create okf staging dir: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(staging)
	}()

	if err := writeBundle(staging, bundle); err != nil {
		return ExportResult{}, err
	}
	validation, err := ValidateBundle(staging)
	if err != nil {
		return ExportResult{}, err
	}
	if !validation.Conformant {
		return ExportResult{}, fmt.Errorf("staged okf bundle failed validation: %s", strings.Join(validation.Errors, "; "))
	}
	if err := swapBundle(staging, targetAbs); err != nil {
		return ExportResult{}, err
	}

	result := bundle.Stats
	result.Bundle = targetAbs
	result.BrokenInternalLinks = validation.BrokenInternalLinks
	result.OmittedByFilterLinks = len(bundle.Manifest.OmittedLinks)
	return result, nil
}

func exportTopicGraphs(ctx context.Context, cfg config.Config, st *store.Store) ([]topics.TopicMap, error) {
	defs, err := vault.ListTopicDefinitions(cfg)
	if err != nil {
		return nil, fmt.Errorf("list okf topic definitions: %w", err)
	}
	graphs := make([]topics.TopicMap, 0, len(defs))
	for _, def := range defs {
		graph, err := topics.FromDefinition(ctx, st, def)
		if err != nil {
			return nil, fmt.Errorf("build okf topic %q: %w", def.Topic, err)
		}
		graphs = append(graphs, graph)
	}
	return graphs, nil
}

func writeBundle(root string, bundle Bundle) error {
	for _, doc := range bundle.Documents {
		if err := validateBundleRelPath(doc.Path); err != nil {
			return fmt.Errorf("invalid document path %s: %w", doc.Path, err)
		}
		full, err := safeJoin(root, doc.Path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("create okf dir %s: %w", filepath.Dir(full), err)
		}
		var body string
		if filepath.Base(full) == "index.md" {
			body = strings.TrimRight(doc.Body, " \t\r\n") + "\n"
		} else {
			body, err = RenderDocument(doc)
			if err != nil {
				return err
			}
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			return fmt.Errorf("write okf document %s: %w", doc.Path, err)
		}
	}
	data, err := json.MarshalIndent(bundle.Manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode okf manifest: %w", err)
	}
	manifestPath, err := safeJoin(root, manifestFileName)
	if err != nil {
		return err
	}
	if err := os.WriteFile(manifestPath, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write okf manifest: %w", err)
	}
	return nil
}

func acquireLock(parent string) (func(), error) {
	lockPath := filepath.Join(parent, ".dbrain-okf-export.lock")
	lock, err := runlock.Acquire(lockPath, fmt.Sprintf("pid=%d\n", os.Getpid()))
	if errors.Is(err, runlock.ErrAlreadyLocked) {
		return nil, fmt.Errorf("okf export lock already held: %s", lockPath)
	}
	if err != nil {
		return nil, err
	}
	return func() {
		_ = lock.Close()
	}, nil
}

func swapBundle(staging, target string) error {
	if err := rejectSymlink(target); err != nil {
		return err
	}
	backup := target + ".previous"
	_ = os.RemoveAll(backup)
	targetExists := false
	if _, err := os.Lstat(target); err == nil {
		targetExists = true
		if err := os.Rename(target, backup); err != nil {
			return fmt.Errorf("move current okf bundle aside: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect current okf bundle: %w", err)
	}
	if err := os.Rename(staging, target); err != nil {
		if targetExists {
			_ = os.Rename(backup, target)
		}
		return fmt.Errorf("promote staged okf bundle: %w", err)
	}
	if targetExists {
		_ = os.RemoveAll(backup)
	}
	return nil
}

func rejectSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect path %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to write okf bundle through symlink: %s", path)
	}
	return nil
}
