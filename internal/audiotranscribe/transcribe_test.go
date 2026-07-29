package audiotranscribe

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWhisperCPPUsesModelLanguageVADAndFileOutput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	model := writeTestFile(t, dir, "ggml-base.bin", "model")
	vad := writeTestFile(t, dir, "ggml-vad.bin", "vad")
	audio := writeTestFile(t, dir, "audio.wav", "audio")
	argsPath := filepath.Join(dir, "args")
	binary := writeExecutable(t, dir, "whisper-cli", `#!/bin/sh
printf '%s\n' "$@" > "`+argsPath+`"
out=''
prev=''
for arg in "$@"; do
  if [ "$prev" = '-of' ]; then out="$arg"; fi
  prev="$arg"
done
printf '%s\n' 'the transcript is long enough for production use' > "${out}.txt"
`)

	result, err := Transcribe(context.Background(), audio, Config{
		Backend:             BackendWhisperCPP,
		Language:            "en",
		WhisperBinary:       binary,
		WhisperModelPath:    model,
		WhisperVADModelPath: vad,
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if result.Backend != BackendWhisperCPP || result.Model != "ggml-base.bin" || !result.VADEnabled || result.NoSpeech {
		t.Fatalf("unexpected result: %+v", result)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	for _, want := range []string{"-m\n" + model, "-l\nen", "-nt", "-np", "--vad\n--vad-model\n" + vad, "-otxt", "-of", "-f\n" + audio} {
		if !strings.Contains(string(args), want) {
			t.Fatalf("args missing %q:\n%s", want, args)
		}
	}
}

func TestWhisperCPPEmptyOutputIsNoSpeech(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	model := writeTestFile(t, dir, "model.bin", "model")
	audio := writeTestFile(t, dir, "audio.wav", "audio")
	binary := writeExecutable(t, dir, "whisper-cli", `#!/bin/sh
out=''
prev=''
for arg in "$@"; do
  if [ "$prev" = '-of' ]; then out="$arg"; fi
  prev="$arg"
done
: > "${out}.txt"
`)
	result, err := Transcribe(context.Background(), audio, Config{Backend: BackendWhisperCPP, WhisperBinary: binary, WhisperModelPath: model})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if !result.NoSpeech || result.Text != "" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestMacWhisperPlaceholderIsNoSpeech(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	audio := writeTestFile(t, dir, "audio.wav", "audio")
	binary := writeExecutable(t, dir, "mw", "#!/bin/sh\nprintf '%s\\n' '[BLANK_AUDIO]'\n")
	result, err := Transcribe(context.Background(), audio, Config{Backend: "macwhisper:test-model", MacWhisperBinary: binary})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if !result.NoSpeech || result.Text != "" || result.Model != "test-model" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestMacWhisperReportsConfiguredAndAutomaticModelSelectionHonestly(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		backend      string
		wantModel    string
		wantModelArg bool
	}{
		{name: "configured", backend: "macwhisper:whisperkit:openai_whisper-base", wantModel: "whisperkit:openai_whisper-base", wantModelArg: true},
		{name: "automatic", backend: BackendMacWhisper, wantModel: ModelSelectionAutomatic, wantModelArg: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			audio := writeTestFile(t, dir, "audio.wav", "audio")
			argsPath := filepath.Join(dir, "args")
			binary := writeExecutable(t, dir, "mw", "#!/bin/sh\nprintf '%s\\n' \"$@\" > \""+argsPath+"\"\nprintf '%s\\n' 'a transcript long enough to be accepted by the caller'\n")

			result, err := Transcribe(context.Background(), audio, Config{Backend: tc.backend, MacWhisperBinary: binary})
			if err != nil {
				t.Fatalf("Transcribe: %v", err)
			}
			if result.Backend != BackendMacWhisper || result.Model != tc.wantModel {
				t.Fatalf("unexpected result: %+v", result)
			}
			args, err := os.ReadFile(argsPath)
			if err != nil {
				t.Fatalf("read args: %v", err)
			}
			hasModelArg := strings.Contains(string(args), "--model\n")
			if hasModelArg != tc.wantModelArg {
				t.Fatalf("model argument presence = %v, want %v: %s", hasModelArg, tc.wantModelArg, args)
			}
		})
	}
}

func TestRejectsUnknownBackend(t *testing.T) {
	t.Parallel()
	_, err := Transcribe(context.Background(), "audio.wav", Config{Backend: "cloud"})
	if err == nil || !strings.Contains(err.Error(), "unsupported transcription backend") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func writeTestFile(t *testing.T, dir string, name string, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func writeExecutable(t *testing.T, dir string, name string, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	tmp, err := os.CreateTemp(dir, "."+name+"-*")
	if err != nil {
		t.Fatalf("create temporary executable for %s: %v", path, err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.WriteString(body); err != nil {
		_ = tmp.Close()
		t.Fatalf("write %s: %v", tmpPath, err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		t.Fatalf("chmod %s: %v", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatalf("close %s: %v", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		t.Fatalf("publish %s: %v", path, err)
	}
	return path
}
