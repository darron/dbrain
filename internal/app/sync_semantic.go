package app

import (
	"sync"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/runlock"
	"github.com/darron/dbrain/internal/syncjob"
)

type syncCommandCompleted struct {
	cfg     config.Config
	stats   syncjob.Stats
	jsonOut bool
	lock    *runlock.Lock
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
		_ = stale.lock.Close()
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
