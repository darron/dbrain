# Open-source MacWhisper replacement bakeoff

Date: 2026-07-11  
Machine: Apple M5 Pro  
Boundary: production dbrain database and media were read-only; all generated inputs, models, and results lived in `/private/tmp/dbrain-open-transcriber-bakeoff/.codex/bakeoff`.

## Decision

Use **whisper.cpp 1.9.1 with Metal and Silero VAD** as dbrain's default local transcription backend. Keep the backend pluggable so MacWhisper can remain available during migration and WhisperKit can remain an optional Apple-native challenger.

Confidence: **high** that whisper.cpp is the best of the tested Homebrew-installable open-source choices for dbrain; **moderate** on corpus-wide quality because this was a four-input directional bakeoff rather than a scored large-corpus evaluation.

The decisive evidence was not merely speed:

- whisper.cpp was the fastest complete backend on all speech inputs.
- its runtime confirmed the Homebrew build was using the Apple M5 Pro Metal device.
- it supports explicit language selection; the current `mw` CLI does not.
- on the production X-media sample, MacWhisper auto-detected Vietnamese mid-clip and stopped early, while whisper.cpp completed the full English clip.
- bare Whisper base hallucinated `you` on digital silence in both whisper.cpp and direct WhisperKit. The official Silero VAD eliminated the hallucination and returned an empty transcript.
- OpenAI Whisper's PyTorch MPS path worked but was dramatically slower and more memory-hungry than its CPU path on this machine. It is a reference implementation, not a competitive production backend here.

## Installed software

| Component | Before | After | License |
|---|---:|---:|---|
| `whisper-cpp` | 1.8.4 | 1.9.1 | MIT |
| `whisperkit-cli` | not installed | 1.0.0 | MIT |
| `openai-whisper` | 20250625_4 | unchanged | MIT |
| MacWhisper | 13.22.0 (1432) | unchanged | proprietary baseline |

MacWhisper reported its selected model as `whisperkit:openai_whisper-base` (145 MB). The matched candidate models were:

- whisper.cpp: multilingual `ggml-base.bin`, SHA-256 `60ed5bc3dd14eea856493d334349b405782ddcaf0028d4b5df4088345fba2efe`
- direct WhisperKit: `argmaxinc/whisperkit-coreml/openai_whisper-base`
- OpenAI Whisper: `base.pt`, SHA-256 `ed3a0b6b1c0edf879ad9b11b1af5a0e6ab5db9205f891f668f8b0e6c6326e34e`
- VAD: `ggml-silero-v6.2.0.bin`

## Acceleration verification

| Backend | Acceleration used | Verification |
|---|---|---|
| MacWhisper | WhisperKit/Core ML baseline | selected model ID is `whisperkit:openai_whisper-base` |
| whisper.cpp | Metal | runtime loaded the Metal backend and identified `MTL0 (Apple M5 Pro)` |
| direct WhisperKit | Core ML CPU + Neural Engine | encoder and decoder explicitly set to `cpuAndNeuralEngine` |
| OpenAI Whisper | CPU reference | faster than its functional PyTorch MPS path in this build |

OpenAI Whisper MPS on the 11-second JFK clip took 18.27 seconds and about 2.07 GB maximum RSS. Its cached CPU run took 1.95 seconds and about 913 MB maximum RSS. MPS was therefore excluded from the remaining timing table; using it would satisfy an acceleration checkbox while making the real system worse.

## Inputs

Every source was converted once to mono 16 kHz PCM WAV and the exact same WAV was passed to every backend.

| Input | Duration | Purpose | SHA-256 |
|---|---:|---|---|
| JFK | 11.00 s | clean speech with known reference | `594367714987c522419e2813e9221192aadceee78ae1fbcc1cb3f2684a772627` |
| Personal scam call | 39.34 s | telephone-quality speech, names and phone number | `875fd38aec0189bbb45e2c561d66920b666a62ce5343395142d9643e6e093afa` |
| Production X sample | 60.00 s | real dbrain X-media audio with abrupt speaker/topic edits | `ee86dd14242959f17cb705fb21e755f9082883d21deff910d6cb48b08f80e859` |
| Digital silence | 10.00 s | hallucination/no-speech control | `d5d99f67ec2dcdba6875a181e4069c79f170241ed28238f5ff76c25a37f3720f` |

## Warm timing results

These are directional single warm runs, not multi-run medians. First-run model downloads and compilation are excluded. RTF is wall time divided by audio duration; lower is better.

| Input | Backend | Wall time | RTF | Maximum RSS | Complete/useful? |
|---|---|---:|---:|---:|---|
| JFK | MacWhisper | 1.02 s | 0.093 | CLI wrapper only | yes |
| JFK | whisper.cpp + Metal | **0.29 s** | **0.026** | 365 MB | yes |
| JFK | direct WhisperKit + ANE | 3.49 s | 0.317 | 131 MB | yes |
| JFK | OpenAI Whisper CPU | 1.95 s | 0.177 | 913 MB | yes |
| Personal | MacWhisper | 1.14 s | 0.029 | CLI wrapper only | yes, plus `[BLANK_AUDIO]` marker |
| Personal | whisper.cpp + Metal + VAD | **0.46 s** | **0.012** | 387 MB | yes |
| Personal | direct WhisperKit + ANE | 2.98 s | 0.076 | 175 MB | inserted a false repeated phone-number fragment |
| Personal | OpenAI Whisper CPU | 3.96 s | 0.101 | 1.04 GB | yes |
| Production X | MacWhisper | 1.56 s | 0.026 | CLI wrapper only | **no: wrong language switch and early stop** |
| Production X | whisper.cpp + Metal + VAD | **0.80 s** | **0.013** | 407 MB | yes |
| Production X | direct WhisperKit + ANE | 3.32 s | 0.055 | 220 MB | yes |
| Production X | OpenAI Whisper CPU | 11.88 s | 0.198 | 1.36 GB | yes |
| Silence | MacWhisper | 0.60 s | 0.060 | CLI wrapper only | returned `[BLANK_AUDIO]` |
| Silence | whisper.cpp, no VAD | 0.22 s | 0.022 | 359 MB | hallucinated `you` |
| Silence | direct WhisperKit + ANE | 2.75 s | 0.275 | 131 MB | hallucinated `you` |
| Silence | whisper.cpp + Metal + VAD | **0.18 s** | **0.018** | 304 MB | **correctly empty** |

MacWhisper memory numbers are intentionally omitted: `/usr/bin/time` measured the small `mw` client, not the already-resident MacWhisper application and model.

Direct WhisperKit's first JFK run took 31.96 seconds because model acquisition and first-run Core ML setup were included. Its cached warm run took 3.49 seconds.

## Quality observations

### JFK

All four backends produced the same words after lowercasing and punctuation removal, giving loose WER 0 against:

> And so my fellow Americans, ask not what your country can do for you, ask what you can do for your country.

### Personal telephone clip

All backends captured the main content, `Officer Jonathan Knight`, `Revenue Canada`, and `613-699-4491`. Direct WhisperKit falsely repeated the last part of the phone number once. MacWhisper emitted `[BLANK_AUDIO]` after the spoken content. whisper.cpp with VAD produced the cleanest complete output in the shortest time.

### Production X-media clip

The clip contains multiple edited speech segments. MacWhisper produced the Kubernetes/Docker opening, then switched to Vietnamese-like text and stopped early. Its CLI exposes model selection but no language option. The three other backends completed the clip when explicitly set to English. All base-size models struggled with phrases resembling `Gen AI`, but whisper.cpp remained comparable to the reference outputs while being much faster.

### Silence

The underlying base model hallucinated `you` in both bare whisper.cpp and direct WhisperKit. MacWhisper post-processed silence into `[BLANK_AUDIO]`, which is still a non-empty string and would be unsafe to persist as evidence without special handling. Silero VAD prevented inference on the silent region and returned no transcript.

## Recommended production command shape

```sh
whisper-cli \
  -m "$WHISPER_MODEL_PATH" \
  -l "$LANGUAGE" \
  -nt \
  -np \
  --vad \
  --vad-model "$WHISPER_VAD_MODEL_PATH" \
  -f "$AUDIO_WAV"
```

For dbrain integration, file output is safer than stdout because backend diagnostics can otherwise contaminate transcript text:

```sh
whisper-cli \
  -m "$WHISPER_MODEL_PATH" \
  -l "$LANGUAGE" \
  -nt \
  -np \
  --vad \
  --vad-model "$WHISPER_VAD_MODEL_PATH" \
  -otxt \
  -of "$OUTPUT_BASE" \
  -f "$AUDIO_WAV"
```

The production adapter should treat an empty VAD result as `blocked/no speech`, not as a retryable error, and explicitly reject known placeholder outputs such as `[BLANK_AUDIO]` if MacWhisper remains supported.

## Recommended dbrain design

Add a narrow transcriber abstraction rather than replacing one hard-coded CLI with another:

- `whisper-cpp`: default; explicit model path, VAD model path, language, Metal enabled by default.
- `macwhisper`: compatibility backend during migration.
- `whisperkit`: optional experimental backend.
- `openai-whisper`: bakeoff/reference only.

Reuse the existing YouTube `whisper-cli` implementation, but centralize it so X media and YouTube do not maintain different command contracts. Pre-extract a deterministic mono 16 kHz WAV for every backend. Persist backend/model/VAD provenance separately from raw transcript text.

Before shipping, run a larger read-only corpus comparison with at least 30 short X-media samples and a few long files. The next gate should measure completion rate, empty/no-speech classification, obvious hallucinations, names/numbers, and warm median/p95 runtime—not only subjective transcript preference.

## Open-source and licensing conclusion

The relevant code paths are genuinely open source:

- [whisper.cpp](https://github.com/ggml-org/whisper.cpp) is MIT licensed and documents Apple Silicon Metal/Core ML support.
- [OpenAI Whisper](https://github.com/openai/whisper) releases both code and official model weights under MIT.
- [Argmax OSS / WhisperKit](https://github.com/argmaxinc/argmax-oss-swift) is MIT licensed and provides the Homebrew CLI.

Apple's Metal, Core ML, and Neural Engine system frameworks are proprietary platform runtimes. That does not make the applications closed source, but it means “every runtime layer is open source” is not literally true for accelerated macOS execution. If that stricter definition matters, whisper.cpp can run CPU-only; for this task, local processing and open-source application/model code were the operative requirements.

Homebrew formula licenses do not automatically establish the license of arbitrary third-party or fine-tuned model weights. Production should pin the exact official model source, revision, checksum, and license, and should not bundle community conversions without separate provenance review.

## Research Pulse

Production run `019f5255-b45c-7414-9e42-1366ddf0ea82` completed `partial_failed` with a usable chairman artifact: GPT, Claude, Gemini, and Qwen succeeded through gathering/review and GPT synthesis; GLM failed with a Z.AI 429 for insufficient provider balance. The full submitted question was preserved (2,836 characters), all four reviews completed, and the final synthesis completed, so no rerun was warranted.

Artifacts:

- `.codex/tmp/open-source-transcriber-pulse.md`
- `.codex/tmp/open-source-transcriber-pulse.json`
