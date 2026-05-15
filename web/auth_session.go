package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"
)

type authUser struct {
	Provider string
	ID       string
	Username string
	Email    string
}

type authSession struct {
	Token     string
	User      authUser
	CreatedAt time.Time
	ExpiresAt time.Time
}

type authSessionStore struct {
	mu       sync.RWMutex
	sessions map[string]authSession
	now      func() time.Time
}

func newAuthSessionStore() *authSessionStore {
	return &authSessionStore{
		sessions: map[string]authSession{},
		now:      time.Now,
	}
}

func (s *authSessionStore) startCleanup(ctx context.Context, interval time.Duration) {
	if ctx == nil || interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.cleanupExpired(s.now())
			}
		}
	}()
}

func (s *authSessionStore) create(user authUser, ttl time.Duration) (authSession, error) {
	token, err := randomToken(32)
	if err != nil {
		return authSession{}, err
	}
	now := s.now()
	session := authSession{
		Token:     token,
		User:      user,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[token] = session
	return session, nil
}

func (s *authSessionStore) get(token string) (authSession, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return authSession{}, false
	}

	s.mu.RLock()
	session, ok := s.sessions[token]
	s.mu.RUnlock()
	if !ok {
		return authSession{}, false
	}
	if !session.ExpiresAt.After(s.now()) {
		s.delete(token)
		return authSession{}, false
	}
	return session, true
}

func (s *authSessionStore) delete(token string) {
	token = strings.TrimSpace(token)
	if token == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
}

func (s *authSessionStore) cleanupExpired(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for token, session := range s.sessions {
		if !session.ExpiresAt.After(now) {
			delete(s.sessions, token)
			removed++
		}
	}
	return removed
}

func randomToken(bytes int) (string, error) {
	if bytes <= 0 {
		return "", fmt.Errorf("random token size must be positive")
	}
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
