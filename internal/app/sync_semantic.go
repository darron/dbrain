package app

import (
	"errors"
	"sync"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/metrics"
	"github.com/darron/dbrain/internal/runlock"
	"github.com/darron/dbrain/internal/syncjob"
)

type syncCommandCompleted struct {
	cfg          config.Config
	stats        syncjob.Stats
	runErr       error
	jsonOut      bool
	semanticGC   bool
	lock         *runlock.Lock
	metrics      metrics.RunContext
	closeMetrics func() error
	ui           *syncProgressUI
}

func (c *syncCommandCompleted) close() error {
	if c == nil {
		return nil
	}
	if c.ui != nil {
		c.ui.Close()
		c.ui = nil
	}
	var closeErr error
	if c.closeMetrics != nil {
		closeErr = errors.Join(closeErr, c.closeMetrics())
		c.closeMetrics = nil
	}
	if c.lock != nil {
		closeErr = errors.Join(closeErr, c.lock.Close())
		c.lock = nil
	}
	return closeErr
}

type syncCommandCompletion struct {
	mu        sync.Mutex
	completed *syncCommandCompleted
}

func (s *syncCommandCompletion) reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	stale := s.completed
	s.completed = nil
	s.mu.Unlock()
	if stale != nil {
		_ = stale.close()
	}
}

func (s *syncCommandCompletion) record(completed syncCommandCompleted) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.completed = &completed
	s.mu.Unlock()
}

func (s *syncCommandCompletion) consume() *syncCommandCompleted {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	completed := s.completed
	s.completed = nil
	s.mu.Unlock()
	return completed
}
