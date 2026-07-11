package install

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/darron/dbrain/internal/config"
)

func prepareOllamaModels(ctx context.Context, fsys FileSystem, cfg config.Config, opts Options) ([]Change, error) {
	if len(opts.OllamaModels) == 0 {
		return nil, nil
	}
	runner := opts.CommandRunner
	if runner == nil {
		runner = OSCommandRunner{}
	}
	changes := []Change{}
	for _, setup := range opts.OllamaModels {
		model := strings.TrimSpace(setup.Model)
		if model == "" {
			return changes, fmt.Errorf("ollama model setup requires a model")
		}
		pullModel := strings.TrimSpace(setup.PullModel)
		if pullModel == "" {
			pullModel = model
		}
		modelfilePath := ""
		if len(setup.Modelfile) > 0 {
			name := strings.TrimSpace(setup.ModelfileName)
			if name == "" {
				name = "Modelfile." + sanitizeInstallFileName(model)
			}
			modelfilePath = filepath.Join(cfg.ConfigDir, name)
			change, err := writeManagedFile(fsys, modelfilePath, setup.Modelfile, 0o644, true, opts.DryRun)
			if err != nil {
				return changes, err
			}
			changes = append(changes, change)
		}
		if opts.DryRun {
			changes = append(changes, Change{Kind: ChangePrepared, Path: "ollama/" + model, Message: "dry run"})
			continue
		}
		if !ollamaModelExists(ctx, runner, pullModel) {
			if err := runInstallCommand(ctx, runner, opts.CommandOutput, "ollama", "pull", pullModel); err != nil {
				return changes, err
			}
		}
		if modelfilePath != "" {
			if err := runInstallCommand(ctx, runner, opts.CommandOutput, "ollama", "create", model, "-f", modelfilePath); err != nil {
				return changes, err
			}
		}
		changes = append(changes, Change{Kind: ChangePrepared, Path: "ollama/" + model})
	}
	return changes, nil
}

func ollamaModelExists(ctx context.Context, runner CommandRunner, model string) bool {
	if strings.TrimSpace(model) == "" {
		return false
	}
	_, err := runner.CombinedOutput(ctx, "ollama", "show", model)
	return err == nil
}

func runInstallCommand(ctx context.Context, runner CommandRunner, outputWriter io.Writer, name string, args ...string) error {
	if outputWriter != nil {
		if streaming, ok := runner.(StreamingCommandRunner); ok {
			_, _ = fmt.Fprintf(outputWriter, "• Running %s %s\n", name, strings.Join(args, " "))
			var captured bytes.Buffer
			output := io.MultiWriter(outputWriter, &captured)
			if err := streaming.Run(ctx, output, output, name, args...); err != nil {
				return installCommandError(name, args, captured.Bytes(), err)
			}
			return nil
		}
	}
	output, err := runner.CombinedOutput(ctx, name, args...)
	if err == nil {
		return nil
	}
	return installCommandError(name, args, output, err)
}

func installCommandError(name string, args []string, output []byte, err error) error {
	command := strings.TrimSpace(name + " " + strings.Join(args, " "))
	text := strings.TrimSpace(string(output))
	if text == "" {
		return fmt.Errorf("%s: %w", command, err)
	}
	return fmt.Errorf("%s: %w: %s", command, err, truncateInstallCommandOutput(text, 2000))
}

func truncateInstallCommandOutput(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "...<truncated>"
}

func sanitizeInstallFileName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "model"
	}
	replacer := strings.NewReplacer("/", "-", ":", "-", "\\", "-", " ", "-")
	return replacer.Replace(value)
}

type OSCommandRunner struct{}

func (OSCommandRunner) CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func (OSCommandRunner) Run(ctx context.Context, stdout, stderr io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}
