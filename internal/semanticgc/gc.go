package semanticgc

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/semanticlock"
	"github.com/darron/dbrain/internal/store"
)

type Catalog interface {
	PlanRetrievalSemanticGC(context.Context, store.RetrievalSemanticGCOptions) (store.RetrievalSemanticGCPlan, error)
	PruneRetrievalSemanticCatalog(context.Context, store.RetrievalSemanticGCOptions) (store.RetrievalSemanticGCPlan, error)
	VacuumRetrievalDatabase(context.Context) error
}

type Options struct {
	Now             time.Time
	GracePeriod     time.Duration
	RetainPublished int
	LockTimeout     time.Duration
	Apply           bool
	Vacuum          bool
}

type Artifact struct {
	Path       string    `json:"path"`
	Kind       string    `json:"kind"`
	ProfileID  string    `json:"profile_id,omitempty"`
	ID         string    `json:"id,omitempty"`
	Bytes      int64     `json:"bytes"`
	ModifiedAt time.Time `json:"modified_at"`
	Orphan     bool      `json:"orphan"`
}

type Result struct {
	Applied                bool                          `json:"applied"`
	Vacuumed               bool                          `json:"vacuumed"`
	Catalog                store.RetrievalSemanticGCPlan `json:"catalog"`
	FilesystemArtifacts    []Artifact                    `json:"filesystem_artifacts"`
	PrunableBytes          int64                         `json:"prunable_bytes"`
	DeletedFilesystemDirs  int                           `json:"deleted_filesystem_dirs"`
	DeletedFilesystemBytes int64                         `json:"deleted_filesystem_bytes"`
}

func Run(ctx context.Context, catalog Catalog, cacheDir, databaseID string, opts Options) (result Result, returnErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if catalog == nil {
		return Result{}, errors.New("semantic GC catalog is nil")
	}
	if opts.Now.IsZero() {
		return Result{}, errors.New("semantic GC current time is required")
	}
	if opts.GracePeriod < 0 || opts.RetainPublished < 0 || opts.LockTimeout < 0 {
		return Result{}, errors.New("semantic GC duration and retention values must not be negative")
	}
	if opts.Vacuum && !opts.Apply {
		return Result{}, errors.New("semantic GC --vacuum requires --apply")
	}
	scope, err := semanticlock.NewScope(cacheDir, databaseID)
	if err != nil {
		return Result{}, fmt.Errorf("configure semantic GC locks: %w", err)
	}
	storeOpts := store.RetrievalSemanticGCOptions{Now: opts.Now, GracePeriod: opts.GracePeriod, RetainPublished: opts.RetainPublished}
	admissionCtx := ctx
	cancelAdmission := func() {}
	if opts.LockTimeout > 0 {
		admissionCtx, cancelAdmission = context.WithTimeout(ctx, opts.LockTimeout)
	}
	defer cancelAdmission()
	if !opts.Apply {
		maintenance, err := scope.AcquireMaintenanceShared(admissionCtx, "owner=semantic-gc\nmode=dry-run\n")
		if err != nil {
			return Result{}, err
		}
		defer closeLease(&returnErr, "release semantic GC maintenance lock", maintenance)
		generation, err := scope.AcquireGenerationShared(admissionCtx, "owner=semantic-gc\nmode=dry-run\n")
		if err != nil {
			return Result{}, err
		}
		defer closeLease(&returnErr, "release semantic GC generation lock", generation)
		cancelAdmission()
		plan, err := catalog.PlanRetrievalSemanticGC(ctx, storeOpts)
		if err != nil {
			return Result{}, err
		}
		artifacts, bytes, err := scanPrunableFilesystem(cacheDir, databaseID, plan, opts.Now.Add(-opts.GracePeriod))
		if err != nil {
			return Result{}, err
		}
		return Result{Catalog: plan, FilesystemArtifacts: artifacts, PrunableBytes: bytes}, nil
	}

	maintenance, err := scope.AcquireMaintenanceExclusive(admissionCtx, "owner=semantic-gc\nmode=apply\n")
	if err != nil {
		return Result{}, err
	}
	defer closeLease(&returnErr, "release semantic GC maintenance lock", maintenance)
	generation, err := maintenance.AcquireGenerationExclusive(admissionCtx, "owner=semantic-gc\nmode=apply\n")
	if err != nil {
		return Result{}, err
	}
	defer closeLease(&returnErr, "release semantic GC generation lock", generation)
	cancelAdmission()
	plan, err := catalog.PruneRetrievalSemanticCatalog(ctx, storeOpts)
	if err != nil {
		return Result{}, err
	}
	result = Result{Applied: true, Catalog: plan}
	artifacts, bytes, err := scanPrunableFilesystem(cacheDir, databaseID, plan, opts.Now.Add(-opts.GracePeriod))
	if err != nil {
		return result, err
	}
	result.FilesystemArtifacts = artifacts
	result.PrunableBytes = bytes
	for _, artifact := range artifacts {
		if err := removeArtifact(cacheDir, databaseID, artifact); err != nil {
			return result, err
		}
		result.DeletedFilesystemDirs++
		result.DeletedFilesystemBytes += artifact.Bytes
	}
	if opts.Vacuum {
		if err := catalog.VacuumRetrievalDatabase(ctx); err != nil {
			return result, err
		}
		result.Vacuumed = true
	}
	return result, nil
}

func closeLease(returnErr *error, operation string, closer interface{ Close() error }) {
	if err := closer.Close(); err != nil {
		*returnErr = errors.Join(*returnErr, fmt.Errorf("%s: %w", operation, err))
	}
}

func scanPrunableFilesystem(cacheDir, databaseID string, plan store.RetrievalSemanticGCPlan, cutoff time.Time) ([]Artifact, int64, error) {
	root, err := semanticDatabaseRoot(cacheDir, databaseID)
	if err != nil {
		return nil, 0, err
	}
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("inspect semantic GC database root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, 0, fmt.Errorf("semantic GC database root is not a real directory: %s", root)
	}

	retained := make(map[string]struct{})
	prunable := make(map[string]Artifact)
	knownProfiles := make(map[string]struct{})
	for _, profileID := range plan.CatalogProfiles {
		knownProfiles[profileID] = struct{}{}
	}
	for _, artifact := range append(append([]store.RetrievalSemanticGCArtifact(nil), plan.RetainedGenerations...), plan.RetainedSegments...) {
		rel, err := expectedArtifactPath(databaseID, artifact)
		if err != nil {
			return nil, 0, err
		}
		retained[rel] = struct{}{}
		knownProfiles[artifact.ProfileID] = struct{}{}
	}
	for _, item := range []struct {
		kind      string
		artifacts []store.RetrievalSemanticGCArtifact
	}{{"generation", plan.PrunableGenerations}, {"segment", plan.PrunableSegments}} {
		for _, artifact := range item.artifacts {
			rel, err := expectedArtifactPath(databaseID, artifact)
			if err != nil {
				return nil, 0, err
			}
			knownProfiles[artifact.ProfileID] = struct{}{}
			prunable[rel] = Artifact{Path: rel, Kind: item.kind, ProfileID: artifact.ProfileID, ID: artifact.ID}
		}
	}

	profileEntries, err := os.ReadDir(root)
	if err != nil {
		return nil, 0, fmt.Errorf("list semantic GC database root: %w", err)
	}
	for _, profileEntry := range profileEntries {
		name := profileEntry.Name()
		if name == "locks" {
			continue
		}
		profileRel := filepath.ToSlash(filepath.Join("semantic", databaseID, name))
		profilePath := filepath.Join(cacheDir, filepath.FromSlash(profileRel))
		profileInfo, err := os.Lstat(profilePath)
		if err != nil {
			return nil, 0, fmt.Errorf("inspect semantic GC profile path %s: %w", profilePath, err)
		}
		if profileInfo.Mode()&os.ModeSymlink != 0 || !profileInfo.IsDir() {
			return nil, 0, fmt.Errorf("semantic GC profile path is not a real directory: %s", profilePath)
		}
		if _, known := knownProfiles[name]; !known {
			newest, err := directoryNewestModTime(profilePath)
			if err != nil {
				return nil, 0, err
			}
			if newest.Before(cutoff) {
				prunable[profileRel] = Artifact{Path: profileRel, Kind: "profile", ProfileID: name, Orphan: true}
			}
			continue
		}
		for _, family := range []string{"generations", "segments"} {
			familyPath := filepath.Join(profilePath, family)
			familyInfo, err := os.Lstat(familyPath)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, 0, fmt.Errorf("inspect semantic GC %s: %w", familyPath, err)
			}
			if familyInfo.Mode()&os.ModeSymlink != 0 || !familyInfo.IsDir() {
				return nil, 0, fmt.Errorf("semantic GC artifact family is not a real directory: %s", familyPath)
			}
			children, err := os.ReadDir(familyPath)
			if err != nil {
				return nil, 0, fmt.Errorf("list semantic GC %s: %w", familyPath, err)
			}
			for _, child := range children {
				rel := filepath.ToSlash(filepath.Join("semantic", databaseID, name, family, child.Name()))
				if _, keep := retained[rel]; keep {
					continue
				}
				path := filepath.Join(cacheDir, filepath.FromSlash(rel))
				childInfo, err := os.Lstat(path)
				if err != nil {
					return nil, 0, fmt.Errorf("inspect semantic GC artifact %s: %w", path, err)
				}
				if childInfo.Mode()&os.ModeSymlink != 0 || !childInfo.IsDir() {
					return nil, 0, fmt.Errorf("semantic GC artifact is not a real directory: %s", path)
				}
				if _, catalogued := prunable[rel]; !catalogued && childInfo.ModTime().Before(cutoff) {
					kind := strings.TrimSuffix(family, "s")
					prunable[rel] = Artifact{Path: rel, Kind: kind, ProfileID: name, ID: child.Name(), Orphan: true}
				}
			}
		}
	}

	artifacts := make([]Artifact, 0, len(prunable))
	var total int64
	for _, artifact := range prunable {
		absolute := filepath.Join(cacheDir, filepath.FromSlash(artifact.Path))
		info, err := os.Lstat(absolute)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, 0, fmt.Errorf("inspect semantic GC candidate %s: %w", absolute, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, 0, fmt.Errorf("semantic GC candidate is not a real directory: %s", absolute)
		}
		artifact.ModifiedAt = info.ModTime().UTC()
		artifact.Bytes, err = directoryBytes(absolute)
		if err != nil {
			return nil, 0, err
		}
		total += artifact.Bytes
		artifacts = append(artifacts, artifact)
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })
	return artifacts, total, nil
}

func expectedArtifactPath(databaseID string, artifact store.RetrievalSemanticGCArtifact) (string, error) {
	family := "segments"
	if strings.Contains(filepath.ToSlash(artifact.RelativeCachePath), "/generations/") {
		family = "generations"
	}
	want := filepath.ToSlash(filepath.Join("semantic", databaseID, artifact.ProfileID, family, artifact.ID))
	if artifact.RelativeCachePath != want {
		return "", fmt.Errorf("semantic GC catalog path %q does not match expected %q", artifact.RelativeCachePath, want)
	}
	return want, nil
}

func semanticDatabaseRoot(cacheDir, databaseID string) (string, error) {
	if strings.TrimSpace(cacheDir) == "" || strings.TrimSpace(databaseID) == "" || databaseID == "." || databaseID == ".." || strings.ContainsAny(databaseID, `/\\`) {
		return "", errors.New("semantic GC cache directory and safe database ID are required")
	}
	absCache, err := filepath.Abs(cacheDir)
	if err != nil {
		return "", fmt.Errorf("resolve semantic GC cache directory: %w", err)
	}
	return filepath.Join(filepath.Clean(absCache), "semantic", databaseID), nil
}

func directoryBytes(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("semantic GC refuses symlink in candidate: %s", path)
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("measure semantic GC candidate %s: %w", root, err)
	}
	return total, nil
}

func directoryNewestModTime(root string) (time.Time, error) {
	var newest time.Time
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("semantic GC refuses symlink in profile: %s", path)
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("inspect semantic GC profile age %s: %w", root, err)
	}
	return newest.UTC(), nil
}

func removeArtifact(cacheDir, databaseID string, artifact Artifact) error {
	root, err := semanticDatabaseRoot(cacheDir, databaseID)
	if err != nil {
		return err
	}
	parts := strings.Split(filepath.ToSlash(artifact.Path), "/")
	if len(parts) < 3 || parts[0] != "semantic" || parts[1] != databaseID {
		return fmt.Errorf("semantic GC deletion target has unexpected path: %s", artifact.Path)
	}
	target := filepath.Join(root, filepath.Join(parts[2:]...))
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("semantic GC deletion target escapes database root: %s", target)
	}
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect semantic GC deletion target %s: %w", target, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("semantic GC deletion target is not a real directory: %s", target)
	}
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("remove semantic GC artifact %s after catalog commit: %w", target, err)
	}
	return nil
}
