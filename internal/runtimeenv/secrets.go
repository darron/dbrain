package runtimeenv

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
)

const SecretCommandTimeout = 10 * time.Second

func FirstNonEmptySecret(ctx context.Context, rootDir string, keys ...string) (string, error) {
	for _, key := range keys {
		if value, ok := Lookup(rootDir, key); ok {
			resolved, err := ResolveSecretRef(ctx, value)
			if err != nil {
				return "", fmt.Errorf("resolve %s secret ref: %w", key, err)
			}
			return resolved, nil
		}
	}
	return "", nil
}

func ResolveSecretRef(ctx context.Context, value string) (string, error) {
	value = strings.TrimSpace(value)
	switch {
	case strings.HasPrefix(value, "env:"):
		key := strings.TrimSpace(strings.TrimPrefix(value, "env:"))
		if key == "" {
			return "", fmt.Errorf("env secret ref missing variable name")
		}
		secret := strings.TrimSpace(os.Getenv(key))
		if secret == "" {
			return "", fmt.Errorf("env secret ref %s is empty", key)
		}
		return secret, nil
	case strings.HasPrefix(value, "op://"):
		return runSecretCommand(ctx, []string{"op", "read", value})
	case strings.HasPrefix(value, "keychain://"):
		service, account, err := parseKeychainRef(value)
		if err != nil {
			return "", err
		}
		secret, err := keyring.Get(service, account)
		if err != nil {
			return "", fmt.Errorf("read keychain secret %s/%s: %w", service, account, err)
		}
		secret = strings.TrimSpace(secret)
		if secret == "" {
			return "", fmt.Errorf("keychain secret %s/%s is empty", service, account)
		}
		return secret, nil
	default:
		return value, nil
	}
}

func parseKeychainRef(ref string) (string, string, error) {
	value := strings.TrimPrefix(strings.TrimSpace(ref), "keychain://")
	parts := strings.SplitN(value, "/", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", fmt.Errorf("keychain ref must be keychain://service/account")
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), nil
}

func runSecretCommand(ctx context.Context, argv []string) (string, error) {
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		return "", fmt.Errorf("secret command is empty")
	}
	ctx, cancel := context.WithTimeout(ctx, SecretCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return "", fmt.Errorf("secret command timed out")
	}
	if err != nil {
		return "", fmt.Errorf("secret command %s failed: %w", argv[0], err)
	}
	secret := strings.TrimSpace(string(out))
	if secret == "" {
		return "", fmt.Errorf("secret command %s returned empty output", argv[0])
	}
	return secret, nil
}
