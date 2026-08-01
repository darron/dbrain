package store

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/semanticlock"
)

func TestAuthoritativeWriteHoldsLeaseThroughCommitAndRollback(t *testing.T) {
	tests := []struct {
		name      string
		writeErr  error
		wantRows  int
		wantError error
	}{
		{name: "commit", wantRows: 1},
		{name: "rollback", writeErr: errors.New("reject write"), wantRows: 0, wantError: errors.New("reject write")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newAuthoritativeWriteTestStore(t)
			var rowsAtRelease int
			st.authoritativeWriteAcquire = func(context.Context, string) (io.Closer, error) {
				return closeFunc(func() error {
					if err := st.db.QueryRow(`SELECT COUNT(*) FROM authoritative_write_test`).Scan(&rowsAtRelease); err != nil {
						return err
					}
					return nil
				}), nil
			}

			_, err := withAuthoritativeWriteTx(t.Context(), st, "test-write", func(ctx context.Context, tx authoritativeWriteTx) (struct{}, error) {
				if _, err := tx.ExecContext(ctx, `INSERT INTO authoritative_write_test (value) VALUES ('written')`); err != nil {
					return struct{}{}, err
				}
				return struct{}{}, tt.writeErr
			})
			if tt.wantError != nil && !errors.Is(err, tt.writeErr) {
				t.Fatalf("withAuthoritativeWriteTx() error = %v, want %v", err, tt.writeErr)
			}
			if tt.wantError == nil && err != nil {
				t.Fatalf("withAuthoritativeWriteTx() error = %v", err)
			}
			if rowsAtRelease != tt.wantRows {
				t.Fatalf("rows observed when lease released = %d, want %d", rowsAtRelease, tt.wantRows)
			}
		})
	}
}

func TestAuthoritativeWriteReportsReleaseError(t *testing.T) {
	st := newAuthoritativeWriteTestStore(t)
	releaseErr := errors.New("release failed")
	st.authoritativeWriteAcquire = func(context.Context, string) (io.Closer, error) {
		return closeFunc(func() error { return releaseErr }), nil
	}

	_, err := withAuthoritativeWriteTx(t.Context(), st, "release-error", func(ctx context.Context, tx authoritativeWriteTx) (struct{}, error) {
		_, err := tx.ExecContext(ctx, `INSERT INTO authoritative_write_test (value) VALUES ('committed')`)
		return struct{}{}, err
	})
	if !errors.Is(err, releaseErr) {
		t.Fatalf("withAuthoritativeWriteTx() error = %v, want release error", err)
	}
	var rows int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM authoritative_write_test`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("committed rows = %d, want 1", rows)
	}
}

func TestNestedAuthoritativeWriteReusesStoreSpecificTransactionAndLease(t *testing.T) {
	st := newAuthoritativeWriteTestStore(t)
	var mu sync.Mutex
	acquires := 0
	releases := 0
	st.authoritativeWriteAcquire = func(context.Context, string) (io.Closer, error) {
		mu.Lock()
		acquires++
		mu.Unlock()
		return closeFunc(func() error {
			mu.Lock()
			releases++
			mu.Unlock()
			return nil
		}), nil
	}

	_, err := withAuthoritativeWriteTx(t.Context(), st, "outer", func(ctx context.Context, tx authoritativeWriteTx) (struct{}, error) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO authoritative_write_test (value) VALUES ('outer')`); err != nil {
			return struct{}{}, err
		}
		return withAuthoritativeWriteTx(ctx, st, "nested", func(ctx context.Context, nested authoritativeWriteTx) (struct{}, error) {
			if nested != tx {
				return struct{}{}, errors.New("nested write did not reuse transaction")
			}
			_, err := nested.ExecContext(ctx, `INSERT INTO authoritative_write_test (value) VALUES ('nested')`)
			return struct{}{}, err
		})
	})
	if err != nil {
		t.Fatalf("nested authoritative write: %v", err)
	}
	if acquires != 1 || releases != 1 {
		t.Fatalf("lease acquire/release = %d/%d, want 1/1", acquires, releases)
	}
	var rows int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM authoritative_write_test`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("committed rows = %d, want 2", rows)
	}
}

func TestAuthoritativeWriteContextTokenIsStoreSpecific(t *testing.T) {
	first := newAuthoritativeWriteTestStore(t)
	second := newAuthoritativeWriteTestStore(t)
	firstAcquires := 0
	secondAcquires := 0
	first.authoritativeWriteAcquire = func(context.Context, string) (io.Closer, error) {
		firstAcquires++
		return closeFunc(func() error { return nil }), nil
	}
	second.authoritativeWriteAcquire = func(context.Context, string) (io.Closer, error) {
		secondAcquires++
		return closeFunc(func() error { return nil }), nil
	}

	_, err := withAuthoritativeWriteTx(t.Context(), first, "first", func(ctx context.Context, tx authoritativeWriteTx) (struct{}, error) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO authoritative_write_test (value) VALUES ('first')`); err != nil {
			return struct{}{}, err
		}
		return withAuthoritativeWriteTx(ctx, second, "second", func(ctx context.Context, tx authoritativeWriteTx) (struct{}, error) {
			_, err := tx.ExecContext(ctx, `INSERT INTO authoritative_write_test (value) VALUES ('second')`)
			return struct{}{}, err
		})
	})
	if err != nil {
		t.Fatalf("cross-store nested authoritative write: %v", err)
	}
	if firstAcquires != 1 || secondAcquires != 1 {
		t.Fatalf("first/second acquires = %d/%d, want 1/1", firstAcquires, secondAcquires)
	}
}

func TestAuthoritativeWriteAcquireAndBeginFailuresDoNotLeakLeaseOrWrite(t *testing.T) {
	t.Run("acquire", func(t *testing.T) {
		st := newAuthoritativeWriteTestStore(t)
		acquireErr := errors.New("acquire failed")
		called := false
		st.authoritativeWriteAcquire = func(context.Context, string) (io.Closer, error) {
			return nil, acquireErr
		}
		_, err := withAuthoritativeWriteTx(t.Context(), st, "acquire-failure", func(context.Context, authoritativeWriteTx) (struct{}, error) {
			called = true
			return struct{}{}, nil
		})
		if !errors.Is(err, acquireErr) {
			t.Fatalf("withAuthoritativeWriteTx() error = %v, want acquire error", err)
		}
		if called {
			t.Fatal("write callback ran after acquisition failure")
		}
	})

	t.Run("begin releases lease", func(t *testing.T) {
		st := newAuthoritativeWriteTestStore(t)
		releases := 0
		st.authoritativeWriteAcquire = func(context.Context, string) (io.Closer, error) {
			return closeFunc(func() error {
				releases++
				return nil
			}), nil
		}
		if err := st.db.Close(); err != nil {
			t.Fatal(err)
		}
		_, err := withAuthoritativeWriteTx(t.Context(), st, "begin-failure", func(context.Context, authoritativeWriteTx) (struct{}, error) {
			return struct{}{}, nil
		})
		if err == nil || !strings.Contains(err.Error(), "begin authoritative write transaction") {
			t.Fatalf("withAuthoritativeWriteTx() error = %v, want begin error", err)
		}
		if releases != 1 {
			t.Fatalf("lease releases after begin failure = %d, want 1", releases)
		}
	})
}

func TestAuthoritativeWritePreservesCallbackAndReleaseErrors(t *testing.T) {
	st := newAuthoritativeWriteTestStore(t)
	writeErr := errors.New("write failed")
	releaseErr := errors.New("release failed")
	st.authoritativeWriteAcquire = func(context.Context, string) (io.Closer, error) {
		return closeFunc(func() error { return releaseErr }), nil
	}
	_, err := withAuthoritativeWriteTx(t.Context(), st, "combined-errors", func(context.Context, authoritativeWriteTx) (struct{}, error) {
		return struct{}{}, writeErr
	})
	if !errors.Is(err, writeErr) || !errors.Is(err, releaseErr) {
		t.Fatalf("withAuthoritativeWriteTx() error = %v, want callback and release errors", err)
	}
}

func TestConfiguredAuthoritativeWritesRespectMaintenanceWhileUnrelatedWritesDoNot(t *testing.T) {
	cacheDir := t.TempDir()
	st, err := OpenWithSemanticCache(filepath.Join(t.TempDir(), "brain.db"), cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	item := model.Item{
		SourceKey:  "item:authoritative-lock",
		SourceType: "test",
		Title:      "before",
		Text:       "projected text",
		NotePath:   "items/authoritative-lock.md",
		UpdatedAt:  time.Now().UTC(),
		LastSeenAt: time.Now().UTC(),
	}
	result, err := st.UpsertItem(t.Context(), item)
	if err != nil {
		t.Fatal(err)
	}

	databaseID, err := st.RetrievalDatabaseID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	scope, err := semanticlock.NewScope(cacheDir, databaseID)
	if err != nil {
		t.Fatal(err)
	}
	exclusive, err := scope.AcquireMaintenanceExclusive(t.Context(), "test-exclusive")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exclusive.Close() })

	if err := st.SaveItemUserTags(t.Context(), result.ItemID, "unrelated-tag"); err != nil {
		t.Fatalf("unrelated tag write blocked by semantic maintenance: %v", err)
	}

	tests := []struct {
		name  string
		write func(context.Context) error
	}{
		{
			name: "item",
			write: func(ctx context.Context) error {
				item.Title = "after"
				item.UpdatedAt = item.UpdatedAt.Add(time.Second)
				_, err := st.UpsertItem(ctx, item)
				return err
			},
		},
		{
			name: "source",
			write: func(ctx context.Context) error {
				_, err := st.UpsertSource(ctx, model.SourceCandidate{
					SourceKey:     "source:authoritative-lock",
					CanonicalURL:  "https://example.test/locked",
					NormalizedURL: "https://example.test/locked",
					SourceType:    "article",
					Domain:        "example.test",
					NotePath:      "sources/authoritative-lock.md",
				})
				return err
			},
		},
		{
			name: "projected enrichment",
			write: func(ctx context.Context) error {
				_, err := st.SaveItemSummary(ctx, result.ItemID, model.SummaryResult{
					Status:    model.ItemSummaryStatusOK,
					Text:      "projected summary",
					FetchedAt: time.Now().UTC(),
				}, "summary-input")
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			if err := tt.write(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("write error = %v, want context deadline while maintenance is exclusive", err)
			}
		})
	}
}

func TestOpenWithSemanticCacheRejectsEmptyCacheDirectory(t *testing.T) {
	st, err := OpenWithSemanticCache(filepath.Join(t.TempDir(), "brain.db"), " ")
	if st != nil {
		_ = st.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "semantic cache directory is empty") {
		t.Fatalf("OpenWithSemanticCache() error = %v, want empty cache directory error", err)
	}
}

func TestProductionWritableStoreCallSitesConfigureSemanticCache(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	dirs := []string{
		filepath.Join(repoRoot, "internal", "app"),
		filepath.Join(repoRoot, "internal", "remote"),
		filepath.Join(repoRoot, "web"),
	}

	var violations []string
	files := token.NewFileSet()
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			parsed, err := parser.ParseFile(files, path, nil, 0)
			if err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
			storeAliases := storeImportAliases(parsed)
			calledSelectors := make(map[token.Pos]bool)
			ast.Inspect(parsed, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
					calledSelectors[selector.Pos()] = true
				}
				return true
			})
			ast.Inspect(parsed, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := selector.X.(*ast.Ident)
				if !ok || !storeAliases[pkg.Name] {
					return true
				}
				switch selector.Sel.Name {
				case "Open", "OpenWithOptions":
					position := files.Position(call.Pos())
					violations = append(violations, fmt.Sprintf("%s:%d uses %s without mandatory semantic cache configuration", path, position.Line, selector.Sel.Name))
				case "OpenWithSemanticCache":
					if len(call.Args) != 2 {
						position := files.Position(call.Pos())
						violations = append(violations, fmt.Sprintf("%s:%d OpenWithSemanticCache has %d arguments, want 2", path, position.Line, len(call.Args)))
					} else if !isSemanticCacheArgument(call.Args[1]) {
						position := files.Position(call.Pos())
						violations = append(violations, fmt.Sprintf("%s:%d OpenWithSemanticCache does not use configured CacheDir", path, position.Line))
					}
				case "OpenWithSemanticCacheOptions":
					if len(call.Args) != 3 {
						position := files.Position(call.Pos())
						violations = append(violations, fmt.Sprintf("%s:%d OpenWithSemanticCacheOptions has %d arguments, want 3", path, position.Line, len(call.Args)))
					} else if !isSemanticCacheArgument(call.Args[1]) {
						position := files.Position(call.Pos())
						violations = append(violations, fmt.Sprintf("%s:%d OpenWithSemanticCacheOptions does not use configured CacheDir", path, position.Line))
					}
				}
				return true
			})
			ast.Inspect(parsed, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if !ok || calledSelectors[selector.Pos()] {
					return true
				}
				pkg, ok := selector.X.(*ast.Ident)
				if !ok || !storeAliases[pkg.Name] {
					return true
				}
				if selector.Sel.Name == "Open" || selector.Sel.Name == "OpenWithOptions" {
					position := files.Position(selector.Pos())
					violations = append(violations, fmt.Sprintf("%s:%d references %s without mandatory semantic cache configuration", path, position.Line, selector.Sel.Name))
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(violations) > 0 {
		t.Fatalf("production writable Store call sites must configure semantic cache:\n%s", strings.Join(violations, "\n"))
	}
}

func storeImportAliases(file *ast.File) map[string]bool {
	aliases := make(map[string]bool)
	for _, spec := range file.Imports {
		if strings.Trim(spec.Path.Value, `"`) != "github.com/darron/dbrain/internal/store" {
			continue
		}
		name := "store"
		if spec.Name != nil {
			name = spec.Name.Name
		}
		aliases[name] = true
	}
	return aliases
}

func isSemanticCacheArgument(expr ast.Expr) bool {
	switch value := expr.(type) {
	case *ast.SelectorExpr:
		return value.Sel.Name == "CacheDir"
	case *ast.Ident:
		return value.Name == "cacheDir"
	default:
		return false
	}
}

func newAuthoritativeWriteTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "brain.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.db.Exec(`CREATE TABLE authoritative_write_test (value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	return st
}

type closeFunc func() error

func (fn closeFunc) Close() error {
	return fn()
}
