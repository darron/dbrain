package semanticgc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/semanticlock"
	"github.com/darron/dbrain/internal/store"
)

func TestRunDryRunFindsCataloguedAndFilesystemOrphansWithoutDeleting(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	databaseID := "db-a"
	now := time.Date(2026, time.August, 6, 4, 0, 0, 0, time.UTC)
	plan := semanticGCTestPlan(databaseID)
	deadPath := writeSemanticGCTestDir(t, cacheDir, plan.PrunableSegments[0].RelativeCachePath, now.Add(-time.Hour))
	orphanPath := writeSemanticGCTestDir(t, cacheDir, filepath.ToSlash(filepath.Join("semantic", databaseID, "profile-a", "generations", "orphan-root")), now.Add(-time.Hour))
	recentPath := writeSemanticGCTestDir(t, cacheDir, filepath.ToSlash(filepath.Join("semantic", databaseID, "profile-a", "segments", "recent-orphan")), now.Add(-time.Minute))
	uncatalogued := writeSemanticGCTestDir(t, cacheDir, filepath.ToSlash(filepath.Join("semantic", databaseID, "old-profile")), now.Add(-time.Hour))
	youngProfile := writeSemanticGCTestDir(t, cacheDir, filepath.ToSlash(filepath.Join("semantic", databaseID, "young-profile", "segments", "young-segment")), now.Add(-time.Minute))
	if err := os.Chtimes(filepath.Join(cacheDir, "semantic", databaseID, "young-profile"), now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	fake := &semanticGCTestCatalog{plan: plan}

	result, err := Run(context.Background(), fake, cacheDir, databaseID, Options{Now: now, GracePeriod: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if result.Applied || fake.pruned {
		t.Fatalf("dry-run mutated catalog: result=%+v fake=%+v", result, fake)
	}
	if len(result.FilesystemArtifacts) != 3 {
		t.Fatalf("filesystem candidates=%+v want catalogued, orphan root, and uncatalogued profile", result.FilesystemArtifacts)
	}
	for _, path := range []string{deadPath, orphanPath, recentPath, uncatalogued, youngProfile} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("dry-run removed %s: %v", path, err)
		}
	}
}

func TestRunApplyCommitsCatalogBeforeRemovingSafeDirectories(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	databaseID := "db-a"
	now := time.Date(2026, time.August, 6, 4, 0, 0, 0, time.UTC)
	plan := semanticGCTestPlan(databaseID)
	deadPath := writeSemanticGCTestDir(t, cacheDir, plan.PrunableSegments[0].RelativeCachePath, now.Add(-time.Hour))
	livePath := writeSemanticGCTestDir(t, cacheDir, plan.RetainedSegments[0].RelativeCachePath, now.Add(-time.Hour))
	orphanPath := writeSemanticGCTestDir(t, cacheDir, filepath.ToSlash(filepath.Join("semantic", databaseID, "profile-a", "generations", "orphan-root")), now.Add(-time.Hour))
	fake := &semanticGCTestCatalog{plan: plan}

	result, err := Run(context.Background(), fake, cacheDir, databaseID, Options{Now: now, GracePeriod: 10 * time.Minute, Apply: true, Vacuum: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied || !result.Vacuumed || !fake.pruned || !fake.vacuumed || result.DeletedFilesystemDirs != 2 {
		t.Fatalf("apply result=%+v fake=%+v", result, fake)
	}
	for _, path := range []string{deadPath, orphanPath} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("apply did not remove %s: %v", path, err)
		}
	}
	if _, err := os.Stat(livePath); err != nil {
		t.Fatalf("apply removed retained segment: %v", err)
	}
}

func TestRunRejectsSymlinkCandidates(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	databaseID := "db-a"
	now := time.Now().UTC()
	plan := semanticGCTestPlan(databaseID)
	target := t.TempDir()
	link := filepath.Join(cacheDir, filepath.FromSlash(plan.PrunableSegments[0].RelativeCachePath))
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), &semanticGCTestCatalog{plan: plan}, cacheDir, databaseID, Options{Now: now})
	if err == nil {
		t.Fatal("Run accepted symlink candidate")
	}
}

func TestRunRejectsSymlinkArtifactFamily(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	databaseID := "db-a"
	profilePath := filepath.Join(cacheDir, "semantic", databaseID, "profile-a")
	if err := os.MkdirAll(profilePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(profilePath, "segments")); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), &semanticGCTestCatalog{plan: semanticGCTestPlan(databaseID)}, cacheDir, databaseID, Options{Now: time.Now().UTC()})
	if err == nil {
		t.Fatal("Run accepted symlink artifact family")
	}
}

func TestRunApplyWaitsForSharedMaintenanceLease(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	databaseID := "db-a"
	scope, err := semanticlock.NewScope(cacheDir, databaseID)
	if err != nil {
		t.Fatal(err)
	}
	holder, err := scope.AcquireMaintenanceShared(context.Background(), "owner=test-holder\n")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = Run(ctx, &semanticGCTestCatalog{}, cacheDir, databaseID, Options{Now: time.Now().UTC(), Apply: true})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run error=%v want deadline while shared maintenance lease is held", err)
	}
}

func TestRemoveArtifactAcceptsRelativeCacheDirectoryWithoutEscapingRoot(t *testing.T) {
	cacheDir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relativeCache, err := filepath.Rel(cwd, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	relativeArtifact := filepath.ToSlash(filepath.Join("semantic", "db-a", "profile-a", "segments", "dead-segment"))
	absolute := writeSemanticGCTestDir(t, cacheDir, relativeArtifact, time.Now().Add(-time.Hour))
	if err := removeArtifact(relativeCache, "db-a", Artifact{Path: relativeArtifact}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(absolute); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("relative-cache removal left artifact: %v", err)
	}
}

type semanticGCTestCatalog struct {
	plan             store.RetrievalSemanticGCPlan
	pruned, vacuumed bool
}

func (f *semanticGCTestCatalog) PlanRetrievalSemanticGC(context.Context, store.RetrievalSemanticGCOptions) (store.RetrievalSemanticGCPlan, error) {
	return f.plan, nil
}

func (f *semanticGCTestCatalog) PruneRetrievalSemanticCatalog(context.Context, store.RetrievalSemanticGCOptions) (store.RetrievalSemanticGCPlan, error) {
	f.pruned = true
	return f.plan, nil
}

func (f *semanticGCTestCatalog) VacuumRetrievalDatabase(context.Context) error {
	f.vacuumed = true
	return nil
}

func semanticGCTestPlan(databaseID string) store.RetrievalSemanticGCPlan {
	return store.RetrievalSemanticGCPlan{
		CatalogProfiles:     []string{"profile-a"},
		RetainedGenerations: []store.RetrievalSemanticGCArtifact{{ID: "live-root", ProfileID: "profile-a", RelativeCachePath: filepath.ToSlash(filepath.Join("semantic", databaseID, "profile-a", "generations", "live-root"))}},
		PrunableGenerations: []store.RetrievalSemanticGCArtifact{{ID: "dead-root", ProfileID: "profile-a", RelativeCachePath: filepath.ToSlash(filepath.Join("semantic", databaseID, "profile-a", "generations", "dead-root"))}},
		RetainedSegments:    []store.RetrievalSemanticGCArtifact{{ID: "live-segment", ProfileID: "profile-a", RelativeCachePath: filepath.ToSlash(filepath.Join("semantic", databaseID, "profile-a", "segments", "live-segment"))}},
		PrunableSegments:    []store.RetrievalSemanticGCArtifact{{ID: "dead-segment", ProfileID: "profile-a", RelativeCachePath: filepath.ToSlash(filepath.Join("semantic", databaseID, "profile-a", "segments", "dead-segment"))}},
	}
}

func writeSemanticGCTestDir(t *testing.T, cacheDir, relative string, modTime time.Time) string {
	t.Helper()
	path := filepath.Join(cacheDir, filepath.FromSlash(relative))
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	payloadPath := filepath.Join(path, "payload")
	if err := os.WriteFile(payloadPath, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(payloadPath, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	return path
}
