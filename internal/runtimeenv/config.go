package runtimeenv

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"gopkg.in/yaml.v3"
)

var (
	registeredConfigFiles sync.Map
	registeredConfigMu    sync.Mutex
)

type registeredConfig struct {
	path      string
	snapshot  map[string]any
	dotenv    map[string]string
	frozen    bool
	previous  *registeredConfig
	temporary bool
	removed   bool
}

func RegisterConfigFile(rootDir string, path string) {
	rootDir = strings.TrimSpace(rootDir)
	path = strings.TrimSpace(path)
	if rootDir == "" || path == "" {
		return
	}
	registeredConfigMu.Lock()
	defer registeredConfigMu.Unlock()
	registeredConfigFiles.Store(rootDir, &registeredConfig{path: path})
}

// RegisterConfigSnapshot temporarily installs already parsed dotenv and config
// maps. While installed, runtime lookups use process environment, then the
// frozen dotenv snapshot, then the frozen YAML snapshot without rereading files.
func RegisterConfigSnapshot(rootDir string, snapshot map[string]any, dotenv map[string]string) func() {
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "" {
		return func() {}
	}
	registeredConfigMu.Lock()
	previousValue, _ := registeredConfigFiles.Load(rootDir)
	previous, _ := previousValue.(*registeredConfig)
	entry := &registeredConfig{
		snapshot: snapshot, dotenv: dotenv, frozen: true,
		previous: previous, temporary: true,
	}
	registeredConfigFiles.Store(rootDir, entry)
	registeredConfigMu.Unlock()
	return func() {
		registeredConfigMu.Lock()
		defer registeredConfigMu.Unlock()
		if entry.removed {
			return
		}
		entry.removed = true
		current, ok := registeredConfigFiles.Load(rootDir)
		if !ok || current != entry {
			return
		}
		replacement := entry.previous
		for replacement != nil && replacement.temporary && replacement.removed {
			replacement = replacement.previous
		}
		if replacement == nil {
			registeredConfigFiles.Delete(rootDir)
			return
		}
		registeredConfigFiles.Store(rootDir, replacement)
	}
}

// LoadConfigSnapshot opens one regular YAML file without following symlinks,
// enforces a streaming byte limit, and honors cancellation during read/parse.
func LoadConfigSnapshot(ctx context.Context, path string, maxBytes int64) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("config snapshot path is required")
	}
	if maxBytes <= 0 {
		return nil, fmt.Errorf("config snapshot byte limit must be positive")
	}
	data, err := readBoundedRegularFile(ctx, path, maxBytes, "config snapshot")
	if err != nil {
		return nil, err
	}
	return parseConfigSnapshot(ctx, data)
}

func readBoundedRegularFile(ctx context.Context, path string, maxBytes int64, label string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", label, err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect %s descriptor: %w", label, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", label)
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("%s exceeds byte limit %d", label, maxBytes)
	}
	type readResult struct {
		data []byte
		err  error
	}
	read := make(chan readResult, 1)
	go func() {
		data, readErr := io.ReadAll(io.LimitReader(file, maxBytes+1))
		read <- readResult{data: data, err: readErr}
	}()
	var result readResult
	select {
	case <-ctx.Done():
		_ = file.Close()
		return nil, ctx.Err()
	case result = <-read:
	}
	if result.err != nil {
		return nil, fmt.Errorf("read %s: %w", label, result.err)
	}
	if int64(len(result.data)) > maxBytes {
		return nil, fmt.Errorf("%s exceeds byte limit %d", label, maxBytes)
	}
	return result.data, nil
}

func parseConfigSnapshot(ctx context.Context, data []byte) (map[string]any, error) {
	parsed := make(chan struct {
		cfg map[string]any
		err error
	}, 1)
	go func() {
		var cfg map[string]any
		parseErr := yaml.Unmarshal(data, &cfg)
		parsed <- struct {
			cfg map[string]any
			err error
		}{cfg: cfg, err: parseErr}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case value := <-parsed:
		if value.err != nil {
			return nil, fmt.Errorf("parse config snapshot: %w", value.err)
		}
		if value.cfg == nil {
			value.cfg = map[string]any{}
		}
		return value.cfg, nil
	}
}

func frozenEnvValue(rootDir, key string) (string, bool) {
	value, ok := registeredConfigFiles.Load(strings.TrimSpace(rootDir))
	if !ok {
		return "", false
	}
	entry, ok := value.(*registeredConfig)
	if !ok || !entry.frozen {
		return "", false
	}
	valueText := strings.TrimSpace(entry.dotenv[strings.TrimSpace(key)])
	return valueText, valueText != ""
}

func loadConfigValueOK(rootDir string, key string) (string, bool) {
	if strings.TrimSpace(rootDir) == "" || strings.TrimSpace(key) == "" {
		return "", false
	}

	cfg, ok := loadConfigForRoot(rootDir)
	if !ok {
		return "", false
	}

	for _, path := range configValuePaths(key) {
		if value, ok := configPathValueOK(cfg, path); ok && value != "" {
			return value, true
		}
	}
	return "", false
}

// ConfigMap returns a shallow copy of a nested YAML map from the runtime config.
// It intentionally does not resolve secret references.
func ConfigMap(rootDir string, path ...string) (map[string]any, bool) {
	if strings.TrimSpace(rootDir) == "" || len(path) == 0 {
		return nil, false
	}
	cfg, ok := loadConfigForRoot(rootDir)
	if !ok {
		return nil, false
	}
	var current any = cfg
	for _, part := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		next, ok := lookupMapValue(m, part)
		if !ok {
			return nil, false
		}
		current = next
	}
	m, ok := current.(map[string]any)
	if !ok {
		return nil, false
	}
	out := make(map[string]any, len(m))
	for key, value := range m {
		out[key] = value
	}
	return out, true
}

func loadConfigList(rootDir string, key string) ([]string, bool) {
	if strings.TrimSpace(rootDir) == "" || strings.TrimSpace(key) == "" {
		return nil, false
	}

	cfg, ok := loadConfigForRoot(rootDir)
	if !ok {
		return nil, false
	}

	for _, path := range configValuePaths(key) {
		if values, ok := configPathList(cfg, path); ok {
			return values, true
		}
		if value, ok := configPathValueOK(cfg, path); ok && value != "" {
			return splitList(value), true
		}
	}
	return nil, false
}

func loadConfigForRoot(rootDir string) (map[string]any, bool) {
	if value, ok := registeredConfigFiles.Load(rootDir); ok {
		if entry, ok := value.(*registeredConfig); ok {
			if entry.frozen {
				return entry.snapshot, true
			}
			if cfg, ok := loadConfigFile(entry.path); ok {
				return cfg, true
			}
		}
	}
	cfg, ok := loadConfigFile(filepath.Join(rootDir, "config.yaml"))
	if !ok {
		cfg, ok = loadConfigFile(filepath.Join(rootDir, "config.yml"))
	}
	return cfg, ok
}

func hasRegisteredConfigSnapshot(rootDir string) bool {
	value, ok := registeredConfigFiles.Load(strings.TrimSpace(rootDir))
	if !ok {
		return false
	}
	entry, ok := value.(*registeredConfig)
	return ok && entry.frozen
}

func loadConfigFile(path string) (map[string]any, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}

	var cfg map[string]any
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, false
	}
	if len(cfg) == 0 {
		return nil, false
	}
	return cfg, true
}

func configPathValueOK(cfg map[string]any, path []string) (string, bool) {
	var current any = cfg
	for _, part := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return "", false
		}
		next, ok := lookupMapValue(m, part)
		if !ok {
			return "", false
		}
		current = next
	}

	switch value := current.(type) {
	case string:
		return strings.TrimSpace(value), true
	case bool:
		return strconv.FormatBool(value), true
	case int:
		return strconv.Itoa(value), true
	case int64:
		return strconv.FormatInt(value, 10), true
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64), true
	default:
		return "", false
	}
}

func configPathList(cfg map[string]any, path []string) ([]string, bool) {
	var current any = cfg
	for _, part := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		next, ok := lookupMapValue(m, part)
		if !ok {
			return nil, false
		}
		current = next
	}

	raw, ok := current.([]any)
	if !ok {
		return nil, false
	}
	values := make([]string, 0, len(raw))
	for _, entry := range raw {
		if value := strings.TrimSpace(fmt.Sprint(entry)); value != "" {
			values = append(values, value)
		}
	}
	return values, true
}

func lookupMapValue(m map[string]any, key string) (any, bool) {
	if value, ok := m[key]; ok {
		return value, true
	}
	lowerKey := strings.ToLower(key)
	for k, value := range m {
		if strings.ToLower(k) == lowerKey {
			return value, true
		}
	}
	return nil, false
}
