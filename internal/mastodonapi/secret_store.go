package mastodonapi

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/zalando/go-keyring"
)

var ErrSecretNotFound = errors.New("mastodon secret not found")

// SecretStore is deliberately reference-oriented so OAuth can write only
// deterministic Keychain refs while tests can use an in-memory implementation.
type SecretStore interface {
	Get(ctx context.Context, ref string) (string, error)
	Put(ctx context.Context, ref string, value string) error
	Delete(ctx context.Context, ref string) error
}

// KeychainSecretStore is the production writer for keychain:// refs.
type KeychainSecretStore struct{}

func (KeychainSecretStore) Get(_ context.Context, ref string) (string, error) {
	service, account, err := keychainServiceAccount(ref)
	if err != nil {
		return "", err
	}
	value, err := keyring.Get(service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrSecretNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read Mastodon Keychain secret: %w", err)
	}
	if strings.TrimSpace(value) == "" {
		return "", ErrSecretNotFound
	}
	return value, nil
}

func (KeychainSecretStore) Put(_ context.Context, ref, value string) error {
	service, account, err := keychainServiceAccount(ref)
	if err != nil {
		return err
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("mastodon secret value is empty")
	}
	if err := keyring.Set(service, account, value); err != nil {
		return fmt.Errorf("write Mastodon Keychain secret: %w", err)
	}
	return nil
}

func (KeychainSecretStore) Delete(_ context.Context, ref string) error {
	service, account, err := keychainServiceAccount(ref)
	if err != nil {
		return err
	}
	if err := keyring.Delete(service, account); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("delete Mastodon Keychain secret: %w", err)
	}
	return nil
}

func keychainServiceAccount(ref string) (string, string, error) {
	if err := ValidateSecretRef(ref); err != nil {
		return "", "", err
	}
	if !strings.HasPrefix(ref, "keychain://") {
		return "", "", fmt.Errorf("oauth writes require a keychain:// secret ref")
	}
	parts := strings.Split(strings.TrimPrefix(ref, "keychain://"), "/")
	return parts[0], parts[1], nil
}

// PersistOAuthSecrets commits token/client values as one logical operation.
// Any partial write restores existing values and removes new entries.
func PersistOAuthSecrets(ctx context.Context, store SecretStore, account AccountConfig, accessToken, clientID, clientSecret string) error {
	if store == nil {
		return fmt.Errorf("mastodon secret store is required")
	}
	values := []struct {
		ref   string
		value string
		name  string
	}{
		{ref: account.AccessTokenRef, value: accessToken, name: "access token"},
		{ref: account.ClientIDRef, value: clientID, name: "client ID"},
		{ref: account.ClientSecretRef, value: clientSecret, name: "client secret"},
	}
	snapshots := make([]secretSnapshot, len(values))
	seen := make(map[string]struct{}, len(values))
	for i, value := range values {
		if err := validateOAuthWriteRef(value.name, value.ref); err != nil {
			return err
		}
		if _, exists := seen[value.ref]; exists {
			return fmt.Errorf("mastodon secret refs must be distinct")
		}
		seen[value.ref] = struct{}{}
		if strings.TrimSpace(value.value) == "" {
			return fmt.Errorf("%s is empty", value.name)
		}
		old, err := store.Get(ctx, value.ref)
		switch {
		case err == nil:
			snapshots[i] = secretSnapshot{ref: value.ref, value: old, exists: true}
		case errors.Is(err, ErrSecretNotFound):
			snapshots[i] = secretSnapshot{ref: value.ref}
		default:
			return fmt.Errorf("read existing %s: %w", value.name, err)
		}
	}

	for i, value := range values {
		if err := store.Put(ctx, value.ref, value.value); err != nil {
			rollbackErr := rollbackSecrets(ctx, store, snapshots[:i+1])
			if rollbackErr != nil {
				return fmt.Errorf("write %s: %v; rollback failed: %w", value.name, err, rollbackErr)
			}
			return fmt.Errorf("write %s: %w", value.name, err)
		}
	}
	return nil
}

func validateOAuthWriteRefs(account AccountConfig) error {
	for _, value := range []struct {
		name string
		ref  string
	}{
		{name: "access token", ref: account.AccessTokenRef},
		{name: "client ID", ref: account.ClientIDRef},
		{name: "client secret", ref: account.ClientSecretRef},
	} {
		if err := validateOAuthWriteRef(value.name, value.ref); err != nil {
			return err
		}
	}
	return nil
}

func validateOAuthWriteRef(name, ref string) error {
	if err := ValidateSecretRef(ref); err != nil {
		return fmt.Errorf("validate %s ref: %w", name, err)
	}
	if !strings.HasPrefix(ref, "keychain://") {
		return fmt.Errorf("oauth writes require a keychain:// ref for %s", name)
	}
	return nil
}

type secretSnapshot struct {
	ref    string
	value  string
	exists bool
}

func rollbackSecrets(ctx context.Context, store SecretStore, snapshots []secretSnapshot) error {
	var firstErr error
	for _, snapshot := range snapshots {
		var err error
		if snapshot.exists {
			err = store.Put(ctx, snapshot.ref, snapshot.value)
		} else {
			err = store.Delete(ctx, snapshot.ref)
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
