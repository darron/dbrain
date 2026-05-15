package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const authProviderGitHub = "github"

type AuthUser struct {
	ID                       int64
	Provider                 string
	GitHubID                 string
	GitHubUsername           string
	GitHubUsernameNormalized string
	Email                    string
	Name                     string
	AvatarURL                string
	ApprovedAt               string
	LastLoginAt              string
	CreatedAt                string
	UpdatedAt                string
}

type GitHubAuthProfile struct {
	GitHubID       string
	GitHubUsername string
	Email          string
	Name           string
	AvatarURL      string
}

// NormalizeGitHubUsername returns the case-insensitive GitHub login key used for auth lookups.
func NormalizeGitHubUsername(username string) string {
	username = strings.TrimSpace(username)
	username = strings.TrimPrefix(username, "@")
	return strings.ToLower(strings.TrimSpace(username))
}

func cleanGitHubUsername(username string) string {
	username = strings.TrimSpace(username)
	username = strings.TrimPrefix(username, "@")
	return strings.TrimSpace(username)
}

func (s *Store) ApproveGitHubAuthUser(ctx context.Context, username string) (AuthUser, bool, error) {
	cleaned := cleanGitHubUsername(username)
	normalized := NormalizeGitHubUsername(username)
	if normalized == "" {
		return AuthUser{}, false, fmt.Errorf("github username is required")
	}

	existing, found, err := s.GetGitHubAuthUserByUsername(ctx, normalized)
	if err != nil {
		return AuthUser{}, false, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if found {
		if cleaned != "" && cleaned != existing.GitHubUsername {
			if _, err := s.db.ExecContext(ctx, `
				UPDATE auth_users
				SET github_username = ?, updated_at = ?
				WHERE id = ?`,
				cleaned,
				now,
				existing.ID,
			); err != nil {
				return AuthUser{}, false, fmt.Errorf("update approved github username: %w", err)
			}
			existing, _, err = s.GetGitHubAuthUserByUsername(ctx, normalized)
			if err != nil {
				return AuthUser{}, false, err
			}
		}
		return existing, false, nil
	}

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO auth_users (
			provider, github_username, github_username_normalized,
			approved_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)`,
		authProviderGitHub,
		cleaned,
		normalized,
		now,
		now,
		now,
	); err != nil {
		return AuthUser{}, false, fmt.Errorf("approve github auth user: %w", err)
	}

	user, found, err := s.GetGitHubAuthUserByUsername(ctx, normalized)
	if err != nil {
		return AuthUser{}, false, err
	}
	if !found {
		return AuthUser{}, false, fmt.Errorf("approved github auth user was not readable after insert")
	}
	return user, true, nil
}

func (s *Store) GetGitHubAuthUserByID(ctx context.Context, githubID string) (AuthUser, bool, error) {
	githubID = strings.TrimSpace(githubID)
	if githubID == "" {
		return AuthUser{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT `+authUserColumns+`
		FROM auth_users
		WHERE provider = ? AND github_id = ?
		LIMIT 1`,
		authProviderGitHub,
		githubID,
	)
	user, err := scanAuthUser(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AuthUser{}, false, nil
		}
		return AuthUser{}, false, fmt.Errorf("load github auth user by id: %w", err)
	}
	return user, true, nil
}

func (s *Store) GetGitHubAuthUserByUsername(ctx context.Context, username string) (AuthUser, bool, error) {
	normalized := NormalizeGitHubUsername(username)
	if normalized == "" {
		return AuthUser{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT `+authUserColumns+`
		FROM auth_users
		WHERE provider = ? AND github_username_normalized = ?
		LIMIT 1`,
		authProviderGitHub,
		normalized,
	)
	user, err := scanAuthUser(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AuthUser{}, false, nil
		}
		return AuthUser{}, false, fmt.Errorf("load github auth user by username: %w", err)
	}
	return user, true, nil
}

func (s *Store) ListGitHubAuthUsers(ctx context.Context) ([]AuthUser, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+authUserColumns+`
		FROM auth_users
		WHERE provider = ?
		ORDER BY github_username_normalized ASC`,
		authProviderGitHub,
	)
	if err != nil {
		return nil, fmt.Errorf("list github auth users: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	users := []AuthUser{}
	for rows.Next() {
		user, err := scanAuthUser(rows)
		if err != nil {
			return nil, fmt.Errorf("scan github auth user: %w", err)
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate github auth users: %w", err)
	}
	return users, nil
}

func (s *Store) RemoveGitHubAuthUser(ctx context.Context, username string) (AuthUser, bool, error) {
	normalized := NormalizeGitHubUsername(username)
	if normalized == "" {
		return AuthUser{}, false, fmt.Errorf("github username is required")
	}

	user, found, err := s.GetGitHubAuthUserByUsername(ctx, normalized)
	if err != nil {
		return AuthUser{}, false, err
	}
	if !found {
		return AuthUser{}, false, nil
	}

	result, err := s.db.ExecContext(ctx, `
		DELETE FROM auth_users
		WHERE id = ? AND provider = ?`,
		user.ID,
		authProviderGitHub,
	)
	if err != nil {
		return AuthUser{}, false, fmt.Errorf("remove github auth user: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return AuthUser{}, false, fmt.Errorf("check removed github auth user: %w", err)
	}
	return user, rows > 0, nil
}

func (s *Store) UpdateGitHubAuthUserFromOAuth(ctx context.Context, userID int64, profile GitHubAuthProfile) (AuthUser, error) {
	if userID <= 0 {
		return AuthUser{}, fmt.Errorf("auth user id is required")
	}
	cleaned := cleanGitHubUsername(profile.GitHubUsername)
	normalized := NormalizeGitHubUsername(profile.GitHubUsername)
	if normalized == "" {
		return AuthUser{}, fmt.Errorf("github username is required")
	}

	githubID := strings.TrimSpace(profile.GitHubID)
	email := strings.TrimSpace(profile.Email)
	name := strings.TrimSpace(profile.Name)
	avatarURL := strings.TrimSpace(profile.AvatarURL)
	now := time.Now().UTC().Format(time.RFC3339)

	result, err := s.db.ExecContext(ctx, `
		UPDATE auth_users
		SET github_id = CASE WHEN ? <> '' THEN ? ELSE github_id END,
			github_username = ?,
			github_username_normalized = ?,
			email = CASE WHEN ? <> '' THEN ? ELSE email END,
			name = CASE WHEN ? <> '' THEN ? ELSE name END,
			avatar_url = CASE WHEN ? <> '' THEN ? ELSE avatar_url END,
			last_login_at = ?,
			updated_at = ?
		WHERE id = ? AND provider = ?`,
		githubID, githubID,
		cleaned,
		normalized,
		email, email,
		name, name,
		avatarURL, avatarURL,
		now,
		now,
		userID,
		authProviderGitHub,
	)
	if err != nil {
		return AuthUser{}, fmt.Errorf("update github auth user from oauth: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return AuthUser{}, fmt.Errorf("check github auth user update: %w", err)
	}
	if rows == 0 {
		return AuthUser{}, fmt.Errorf("github auth user not found: %d", userID)
	}

	row := s.db.QueryRowContext(ctx, `
		SELECT `+authUserColumns+`
		FROM auth_users
		WHERE id = ?`,
		userID,
	)
	user, err := scanAuthUser(row)
	if err != nil {
		return AuthUser{}, fmt.Errorf("reload github auth user after oauth update: %w", err)
	}
	return user, nil
}

const authUserColumns = `
	id, provider, github_id, github_username, github_username_normalized,
	email, name, avatar_url, approved_at, last_login_at, created_at, updated_at`

type authUserScanner interface {
	Scan(dest ...any) error
}

func scanAuthUser(row authUserScanner) (AuthUser, error) {
	var user AuthUser
	if err := row.Scan(
		&user.ID,
		&user.Provider,
		&user.GitHubID,
		&user.GitHubUsername,
		&user.GitHubUsernameNormalized,
		&user.Email,
		&user.Name,
		&user.AvatarURL,
		&user.ApprovedAt,
		&user.LastLoginAt,
		&user.CreatedAt,
		&user.UpdatedAt,
	); err != nil {
		return AuthUser{}, err
	}
	return user, nil
}
