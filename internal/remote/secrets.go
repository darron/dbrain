package remote

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/runtimeenv"
)

const SecretCommandTimeout = 10 * time.Second

type SecretResult struct {
	Value    string
	Source   string
	Warnings []string
}

func ResolveAuthKey(ctx context.Context, opts Options) (SecretResult, error) {
	sources := configuredAuthSources(opts)
	warnings := ignoredSourceWarnings(sources)
	if strings.TrimSpace(opts.AuthKey) != "" {
		return SecretResult{Value: strings.TrimSpace(opts.AuthKey), Source: "auth_key", Warnings: warnings}, nil
	}
	if strings.TrimSpace(opts.AuthKeyRef) != "" {
		value, err := runtimeenv.ResolveSecretRef(ctx, opts.AuthKeyRef)
		if err != nil {
			return SecretResult{}, err
		}
		return SecretResult{Value: value, Source: "auth_key_ref", Warnings: warnings}, nil
	}
	if len(opts.AuthKeyCommand) > 0 {
		if !opts.AllowSecretCommand {
			return SecretResult{}, fmt.Errorf("tsnet.auth_key_command requires tsnet.allow_secret_command=true")
		}
		value, err := runSecretCommand(ctx, opts.AuthKeyCommand)
		if err != nil {
			return SecretResult{}, err
		}
		return SecretResult{Value: value, Source: "auth_key_command", Warnings: warnings}, nil
	}
	return SecretResult{Warnings: warnings}, nil
}

func configuredAuthSources(opts Options) []string {
	var sources []string
	if strings.TrimSpace(opts.AuthKey) != "" {
		sources = append(sources, "auth_key")
	}
	if strings.TrimSpace(opts.AuthKeyRef) != "" {
		sources = append(sources, "auth_key_ref")
	}
	if len(opts.AuthKeyCommand) > 0 {
		sources = append(sources, "auth_key_command")
	}
	return sources
}

func ignoredSourceWarnings(sources []string) []string {
	if len(sources) <= 1 {
		return nil
	}
	warnings := make([]string, 0, len(sources)-1)
	for _, source := range sources[1:] {
		warnings = append(warnings, fmt.Sprintf("ignoring lower-precedence tsnet auth source %s", source))
	}
	return warnings
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
	value := strings.TrimSpace(string(out))
	if value == "" {
		return "", fmt.Errorf("secret command %s returned empty output", argv[0])
	}
	return value, nil
}
