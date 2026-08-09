package mastodonapi

import (
	"context"
	"errors"
	"testing"
)

func TestPersistOAuthSecretsRollsBackPartialKeychainWrites(t *testing.T) {
	store := &testSecretStore{failRef: "keychain://dbrain/client-secret"}
	account := AccountConfig{
		AccessTokenRef:  "keychain://dbrain/access-token",
		ClientIDRef:     "keychain://dbrain/client-id",
		ClientSecretRef: "keychain://dbrain/client-secret",
	}

	err := PersistOAuthSecrets(context.Background(), store, account, "access-token", "client-id", "client-secret")
	if err == nil {
		t.Fatal("PersistOAuthSecrets succeeded despite partial write failure")
	}
	if len(store.values) != 0 {
		t.Fatalf("partial secret writes survived rollback: %#v", store.values)
	}
}

func TestPersistOAuthSecretsPreservesExistingValuesOnFailure(t *testing.T) {
	store := &testSecretStore{
		values:  map[string]string{"keychain://dbrain/access-token": "old-access"},
		failRef: "keychain://dbrain/client-secret",
	}
	account := AccountConfig{
		AccessTokenRef:  "keychain://dbrain/access-token",
		ClientIDRef:     "keychain://dbrain/client-id",
		ClientSecretRef: "keychain://dbrain/client-secret",
	}

	if err := PersistOAuthSecrets(context.Background(), store, account, "new-access", "client-id", "client-secret"); err == nil {
		t.Fatal("PersistOAuthSecrets succeeded despite partial write failure")
	}
	if got := store.values[account.AccessTokenRef]; got != "old-access" {
		t.Fatalf("existing access token = %q, want old value", got)
	}
}

func TestPersistOAuthSecretsRejectsNonKeychainRefs(t *testing.T) {
	account := AccountConfig{
		AccessTokenRef:  "env:MASTODON_ACCESS_TOKEN",
		ClientIDRef:     "keychain://dbrain/client-id",
		ClientSecretRef: "keychain://dbrain/client-secret",
	}
	if err := PersistOAuthSecrets(context.Background(), &testSecretStore{}, account, "access-token", "client-id", "client-secret"); err == nil {
		t.Fatal("PersistOAuthSecrets accepted a non-Keychain access-token ref")
	}
}

type testSecretStore struct {
	values  map[string]string
	failRef string
}

func (s *testSecretStore) Get(_ context.Context, ref string) (string, error) {
	if value, ok := s.values[ref]; ok {
		return value, nil
	}
	return "", ErrSecretNotFound
}

func (s *testSecretStore) Put(_ context.Context, ref, value string) error {
	if ref == s.failRef {
		return errors.New("injected write failure")
	}
	if s.values == nil {
		s.values = map[string]string{}
	}
	s.values[ref] = value
	return nil
}

func (s *testSecretStore) Delete(_ context.Context, ref string) error {
	delete(s.values, ref)
	return nil
}
