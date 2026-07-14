package applenotes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func copyReaderContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buffer := make([]byte, 32<<10)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		read, readErr := src.Read(buffer)
		if read > 0 {
			count, writeErr := dst.Write(buffer[:read])
			written += int64(count)
			if writeErr != nil {
				return written, writeErr
			}
			if count != read {
				return written, io.ErrShortWrite
			}
		}
		if err := ctx.Err(); err != nil {
			return written, err
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return written, nil
			}
			return written, readErr
		}
	}
}

const defaultNotesRelPath = "Library/Group Containers/group.com.apple.notes/NoteStore.sqlite"

func resolveNotesDBPath(override string) (string, error) {
	if strings.TrimSpace(override) != "" {
		return filepath.Abs(strings.TrimSpace(override))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	if strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("resolve home directory: empty HOME")
	}
	return filepath.Join(home, defaultNotesRelPath), nil
}

func notesTripletPaths(dbPath string) []string {
	return []string{dbPath, dbPath + "-wal", dbPath + "-shm"}
}

func copyRegularFileContext(ctx context.Context, source string, dest string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return appleNotesSourcePermissionError(source, fmt.Errorf("open snapshot source %s: %w", source, err))
	}
	defer func() {
		_ = in.Close()
	}()

	info, err := in.Stat()
	if err != nil {
		return appleNotesSourcePermissionError(source, fmt.Errorf("stat snapshot source %s: %w", source, err))
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("snapshot source %s is not a regular file", source)
	}

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create snapshot file %s: %w", dest, err)
	}
	defer func() {
		_ = out.Close()
	}()

	if _, err := copyReaderContext(ctx, out, in); err != nil {
		return fmt.Errorf("copy snapshot file %s to %s: %w", source, dest, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return fmt.Errorf("sync snapshot file %s: %w", dest, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func appleNotesSourcePermissionError(source string, err error) error {
	if err == nil || !errors.Is(err, os.ErrPermission) {
		return err
	}
	return fmt.Errorf("%w\n\nApple Notes import could not read the Notes SQLite store because macOS denied access.\nGrant Full Disk Access to the app or executable macOS associates with this run, then quit and reopen it before retrying.\nPath: System Settings > Privacy & Security > Full Disk Access\nTry adding the dbrain binary first if macOS allows it. If access still fails, grant the parent terminal/IDE instead, such as Terminal, iTerm2, Ghostty, Warp, VS Code, or Codex. Local rebuilds may invalidate binary-specific grants.\nSource: %s", err, source)
}

func sameFile(left, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return os.SameFile(leftInfo, rightInfo)
}

func cleanupForSnapshot(dir string, keep bool) snapshotCleanup {
	return func() error {
		if keep || strings.TrimSpace(dir) == "" {
			return nil
		}
		return os.RemoveAll(dir)
	}
}
