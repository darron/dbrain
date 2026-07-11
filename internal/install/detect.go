package install

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type DetectionOptions struct {
	GOOS    string
	HomeDir string
	Prober  Prober
	Timeout time.Duration
}

type Prober interface {
	LookPath(name string) (string, error)
	PathExists(path string) bool
	Get(ctx context.Context, url string) ([]byte, error)
}

type OSProber struct {
	Client *http.Client
}

func (OSProber) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}

func (OSProber) PathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (p OSProber) Get(ctx context.Context, url string) ([]byte, error) {
	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return ioReadAllLimit(resp.Body, 1<<20)
}

func DetectTools(ctx context.Context, opts DetectionOptions) []Tool {
	if strings.TrimSpace(opts.GOOS) == "" {
		opts.GOOS = runtime.GOOS
	}
	if strings.TrimSpace(opts.HomeDir) == "" {
		if home, err := os.UserHomeDir(); err == nil {
			opts.HomeDir = home
		}
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 500 * time.Millisecond
	}
	if opts.Prober == nil {
		opts.Prober = OSProber{Client: &http.Client{Timeout: opts.Timeout}}
	}

	tools := []Tool{
		detectCommand(opts.Prober, ToolMacWhisper, "MacWhisper CLI", "mw"),
		detectCommand(opts.Prober, ToolOllama, "Ollama CLI", "ollama"),
		detectCommand(opts.Prober, ToolLMStudio, "LM Studio CLI", "lms"),
		detectCommand(opts.Prober, ToolOMLX, "oMLX CLI", "omlx"),
		detectCommand(opts.Prober, ToolTesseract, "Tesseract", "tesseract"),
		detectCommand(opts.Prober, ToolFFprobe, "ffprobe", "ffprobe"),
		detectCommand(opts.Prober, ToolYTDLP, "yt-dlp", "yt-dlp"),
		detectCommand(opts.Prober, ToolSummarize, "summarize", "summarize"),
		detectCommand(opts.Prober, ToolOnePassword, "1Password CLI", "op"),
		detectCommand(opts.Prober, ToolSecurity, "macOS security CLI", "security"),
	}

	if opts.GOOS == "darwin" {
		tools = append(tools, detectFirstPath(opts.Prober, ToolMacWhisperApp, "MacWhisper app", []string{
			"/Applications/MacWhisper.app",
			filepath.Join(opts.HomeDir, "Applications", "MacWhisper.app"),
		}))
		if tool := detectFirstPath(opts.Prober, ToolLMStudio, "LM Studio CLI", []string{
			filepath.Join(opts.HomeDir, ".lmstudio", "bin", "lms"),
		}); tool.Available {
			tools = upsertAvailableTool(tools, tool)
		}
	}

	tools = append(tools,
		detectOpenAIModels(ctx, opts, ToolLMStudioAPI, "LM Studio API", "http://127.0.0.1:1234/v1/models"),
		detectOpenAIModels(ctx, opts, ToolOMLXAPI, "oMLX API", "http://127.0.0.1:8000/v1/models"),
		detectOllamaModels(ctx, opts, "http://127.0.0.1:11434/api/tags"),
	)

	return tools
}

func detectCommand(prober Prober, id ToolID, name string, command string) Tool {
	path, err := prober.LookPath(command)
	if err != nil || strings.TrimSpace(path) == "" {
		return Tool{ID: id, Name: name, Available: false, Detail: command + " not found"}
	}
	return Tool{ID: id, Name: name, Available: true, Path: path}
}

func detectFirstPath(prober Prober, id ToolID, name string, paths []string) Tool {
	for _, path := range paths {
		if prober.PathExists(path) {
			return Tool{ID: id, Name: name, Available: true, Path: path}
		}
	}
	return Tool{ID: id, Name: name, Available: false, Detail: "not found"}
}

func detectOpenAIModels(ctx context.Context, opts DetectionOptions, id ToolID, name string, endpoint string) Tool {
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	raw, err := opts.Prober.Get(ctx, endpoint)
	if err != nil {
		return Tool{ID: id, Name: name, Available: false, Endpoint: endpoint, Error: err.Error()}
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Tool{ID: id, Name: name, Available: false, Endpoint: endpoint, Error: err.Error()}
	}
	models := make([]string, 0, len(payload.Data))
	for _, model := range payload.Data {
		if id := strings.TrimSpace(model.ID); id != "" {
			models = append(models, id)
		}
	}
	sort.Strings(models)
	return Tool{ID: id, Name: name, Available: true, Endpoint: endpoint, Models: models}
}

func detectOllamaModels(ctx context.Context, opts DetectionOptions, endpoint string) Tool {
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	raw, err := opts.Prober.Get(ctx, endpoint)
	if err != nil {
		return Tool{ID: ToolOllamaAPI, Name: "Ollama API", Available: false, Endpoint: endpoint, Error: err.Error()}
	}
	var payload struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Tool{ID: ToolOllamaAPI, Name: "Ollama API", Available: false, Endpoint: endpoint, Error: err.Error()}
	}
	models := make([]string, 0, len(payload.Models))
	for _, model := range payload.Models {
		if name := strings.TrimSpace(model.Name); name != "" {
			models = append(models, name)
		}
	}
	sort.Strings(models)
	return Tool{ID: ToolOllamaAPI, Name: "Ollama API", Available: true, Endpoint: endpoint, Models: models}
}

func ioReadAllLimit(r io.Reader, limit int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, limit))
}

func upsertAvailableTool(tools []Tool, replacement Tool) []Tool {
	for i, tool := range tools {
		if tool.ID != replacement.ID {
			continue
		}
		if !tool.Available {
			tools[i] = replacement
		}
		return tools
	}
	return append(tools, replacement)
}
