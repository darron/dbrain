package runlock

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	writerTicketDigits = 20
	intentCleanupGrace = 25 * time.Millisecond
)

type writerIntent struct {
	lockPath   string
	ticketPath string
	sequence   uint64
	ticket     *heldFile
}

type liveWriterIntent struct {
	path     string
	sequence uint64
}

func acquireSharedContext(ctx context.Context, path string) (*Lock, error) {
	for {
		coordinator, err := acquireCoordinator(ctx, path)
		if err != nil {
			return nil, err
		}
		intents, err := cleanWriterIntents(path, "")
		if err != nil {
			_ = coordinator.close()
			return nil, err
		}
		if len(intents) != 0 {
			_ = coordinator.close()
			if err := waitForRetry(ctx); err != nil {
				return nil, err
			}
			continue
		}

		local, ok := tryAcquireProcessGate(path, Shared)
		if !ok {
			_ = coordinator.close()
			if err := waitForRetry(ctx); err != nil {
				return nil, err
			}
			continue
		}
		file, err := tryAcquireFileLock(path, Shared)
		if err != nil {
			local.release()
			_ = coordinator.close()
			if errors.Is(err, ErrAlreadyLocked) {
				if err := waitForRetry(ctx); err != nil {
					return nil, err
				}
				continue
			}
			return nil, err
		}

		// Recheck while the coordinator still prevents new writer intent. This
		// also catches an intent that survived cleanup between attempts.
		intents, err = cleanWriterIntents(path, "")
		if err != nil || len(intents) != 0 {
			_ = file.close()
			local.release()
			_ = coordinator.close()
			if err != nil {
				return nil, err
			}
			if err := waitForRetry(ctx); err != nil {
				return nil, err
			}
			continue
		}
		if err := coordinator.close(); err != nil {
			_ = file.close()
			local.release()
			return nil, err
		}
		return &Lock{path: path, file: file, local: local}, nil
	}
}

func acquireExclusiveContext(ctx context.Context, path string, metadata string) (_ *Lock, resultErr error) {
	intent, err := createWriterIntent(ctx, path, metadata)
	if err != nil {
		return nil, err
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, intent.closeAfterAcquireFailure())
		}
	}()

	for {
		coordinator, err := acquireCoordinator(ctx, path)
		if err != nil {
			return nil, err
		}
		intents, err := cleanWriterIntents(path, intent.ticketPath)
		if err != nil {
			_ = coordinator.close()
			return nil, err
		}
		if len(intents) == 0 || intents[0].path != intent.ticketPath {
			_ = coordinator.close()
			if err := waitForRetry(ctx); err != nil {
				return nil, err
			}
			continue
		}

		local, ok := tryAcquireProcessGate(path, Exclusive)
		if !ok {
			_ = coordinator.close()
			if err := waitForRetry(ctx); err != nil {
				return nil, err
			}
			continue
		}
		file, err := tryAcquireFileLock(path, Exclusive)
		if err != nil {
			local.release()
			_ = coordinator.close()
			if errors.Is(err, ErrAlreadyLocked) {
				if err := waitForRetry(ctx); err != nil {
					return nil, err
				}
				continue
			}
			return nil, err
		}
		if err := replaceFileLockMetadata(file, metadata); err != nil {
			_ = file.close()
			local.release()
			_ = coordinator.close()
			return nil, fmt.Errorf("write lock metadata %s: %w", path, err)
		}
		if err := coordinator.close(); err != nil {
			_ = file.close()
			local.release()
			return nil, err
		}
		return &Lock{path: path, file: file, local: local, intent: intent}, nil
	}
}

func createWriterIntent(ctx context.Context, path string, metadata string) (*writerIntent, error) {
	coordinator, err := acquireCoordinator(ctx, path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = coordinator.close() }()

	liveIntents, err := cleanWriterIntents(path, "")
	if err != nil {
		return nil, err
	}
	rawSequence, err := readFileLockMetadata(coordinator.file)
	if err != nil {
		return nil, fmt.Errorf("read writer coordinator %s: %w", coordinator.path, err)
	}
	var sequence uint64
	if strings.TrimSpace(rawSequence) != "" {
		if parsed, parseErr := strconv.ParseUint(strings.TrimSpace(rawSequence), 10, 64); parseErr == nil {
			sequence = parsed
		}
	}
	for _, intent := range liveIntents {
		if intent.sequence > sequence {
			sequence = intent.sequence
		}
	}
	if sequence == math.MaxUint64 {
		return nil, fmt.Errorf("writer ticket sequence exhausted for %s", path)
	}
	sequence++
	if err := replaceFileLockMetadata(coordinator.file, strconv.FormatUint(sequence, 10)+"\n"); err != nil {
		return nil, fmt.Errorf("write writer coordinator %s: %w", coordinator.path, err)
	}

	ticketPath := writerTicketPath(path, sequence)
	ticket, err := acquireHeldFileContext(ctx, ticketPath, Exclusive)
	if err != nil {
		return nil, fmt.Errorf("acquire writer ticket %s: %w", ticketPath, err)
	}
	if err := replaceFileLockMetadata(ticket.file, metadata); err != nil {
		_ = ticket.close()
		return nil, fmt.Errorf("write writer ticket %s: %w", ticketPath, err)
	}
	return &writerIntent{
		lockPath:   path,
		ticketPath: ticketPath,
		sequence:   sequence,
		ticket:     ticket,
	}, nil
}

func (i *writerIntent) close() error {
	if i == nil || i.ticket == nil {
		return nil
	}
	releaseErr := i.releaseTicket()
	return errors.Join(releaseErr, i.cleanupReleasedTicket(context.Background()))
}

func (i *writerIntent) closeAfterAcquireFailure() error {
	if i == nil || i.ticket == nil {
		return nil
	}
	releaseErr := i.releaseTicket()
	cleanupCtx, cancel := context.WithTimeout(context.Background(), intentCleanupGrace)
	defer cancel()
	cleanupErr := i.cleanupReleasedTicket(cleanupCtx)
	if errors.Is(cleanupErr, context.DeadlineExceeded) || errors.Is(cleanupErr, context.Canceled) {
		cleanupErr = nil
	}
	return errors.Join(releaseErr, cleanupErr)
}

func (i *writerIntent) releaseTicket() error {
	ticketErr := i.ticket.close()
	i.ticket = nil
	return ticketErr
}

func (i *writerIntent) cleanupReleasedTicket(ctx context.Context) error {
	coordinator, err := acquireCoordinator(ctx, i.lockPath)
	if err != nil {
		return err
	}
	_, cleanupErr := cleanWriterIntents(i.lockPath, "")
	return errors.Join(cleanupErr, coordinator.close())
}

func acquireCoordinator(ctx context.Context, path string) (*heldFile, error) {
	return acquireHeldFileContext(ctx, path+".coordinator", Exclusive)
}

func writerTicketPath(path string, sequence uint64) string {
	return fmt.Sprintf("%s.writer-%0*d.intent", path, writerTicketDigits, sequence)
}

func cleanWriterIntents(path string, ownPath string) ([]liveWriterIntent, error) {
	parent := filepath.Dir(path)
	base := filepath.Base(path)
	entries, err := os.ReadDir(parent)
	if err != nil {
		return nil, fmt.Errorf("read writer intent directory %s: %w", parent, err)
	}
	prefix := base + ".writer-"
	var live []liveWriterIntent
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".intent") {
			continue
		}
		rawSequence := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".intent")
		if len(rawSequence) != writerTicketDigits {
			continue
		}
		sequence, parseErr := strconv.ParseUint(rawSequence, 10, 64)
		if parseErr != nil {
			continue
		}
		ticketPath := filepath.Join(parent, name)
		if ticketPath == ownPath {
			live = append(live, liveWriterIntent{path: ticketPath, sequence: sequence})
			continue
		}
		ticket, acquireErr := tryAcquireFileLock(ticketPath, Exclusive)
		if errors.Is(acquireErr, ErrAlreadyLocked) {
			live = append(live, liveWriterIntent{path: ticketPath, sequence: sequence})
			continue
		}
		if acquireErr != nil {
			return nil, fmt.Errorf("inspect writer ticket %s: %w", ticketPath, acquireErr)
		}
		removeErr := removeLockedFile(ticket, ticketPath)
		closeErr := ticket.close()
		if errors.Is(removeErr, os.ErrNotExist) {
			removeErr = nil
		}
		if err := errors.Join(closeErr, removeErr); err != nil {
			return nil, fmt.Errorf("remove abandoned writer ticket %s: %w", ticketPath, err)
		}
	}
	sort.Slice(live, func(left, right int) bool {
		return live[left].sequence < live[right].sequence
	})
	return live, nil
}
