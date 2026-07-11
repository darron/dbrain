package sourceenrich

import (
	"context"

	"github.com/darron/dbrain/internal/audiotranscribe"
)

func transcribeAudioFile(ctx context.Context, audioPath string, opts Options) (audiotranscribe.Result, error) {
	return audiotranscribe.Transcribe(ctx, audioPath, audiotranscribe.Config{
		Backend:             opts.YouTubeTranscriber,
		Language:            opts.TranscriptionLanguage,
		WhisperBinary:       opts.WhisperBinary,
		WhisperModelPath:    opts.WhisperModelPath,
		WhisperVADModelPath: opts.WhisperVADModelPath,
		MacWhisperBinary:    opts.MacWhisperBinary,
	})
}
