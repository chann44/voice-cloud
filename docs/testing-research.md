# Speech live-test research

Checked 2026-11-17 against primary documentation. These are proposed live-test
inputs, not evidence that a paid provider call or local inference succeeded.
Scope: recorded-audio STT, TTS, and one native audio-to-audio endpoint per S2S
request. No external STT → LLM → TTS pipeline.

## Cloud defaults and public catalogs

| Adapter/task | Suggested upstream model | Voice | First language/format |
| --- | --- | --- | --- |
| OpenAI STT | `gpt-transcribe` | — | English WAV input, JSON output |
| OpenAI TTS (legacy endpoint) | `gpt-4o-mini-tts` | `marin` | English, MP3; expires 2027-01-06 |
| Deepgram STT | `nova-3` | — | `en`, WAV input, JSON output |
| Deepgram TTS | `aura-2-thalia-en` | `aura-2-thalia-en` | English, MP3; repeat WAV |
| ElevenLabs STT | `scribe_v3` | — | `en`, WAV input, JSON output |
| ElevenLabs TTS | `eleven_multilingual_v2` | Account-discovered voice ID | `en`, MP3 |
| ElevenLabs S2S | `eleven_multilingual_sts_v2` | Account-discovered voice ID | Same-language English, MP3 |

OpenAI currently recommends `gpt-transcribe` at `/v1/audio/transcriptions`.
`whisper-1`, `gpt-4o-transcribe`, `gpt-4o-mini-transcribe`, and
`gpt-4o-transcribe-diarize` are deprecated, with shutdown documented for
2027-02-26. Keep any existing deployment of those IDs a separately labeled
compatibility test. Upload ceiling is 25 MB; WAV and FLAC are supported.
Source: [OpenAI file transcription](https://developers.openai.com/api/docs/guides/speech-to-text).

OpenAI Speech API models include `gpt-4o-mini-tts`, `tts-1`, `tts-1-hd`.
All three are deprecated with documented shutdown on 2027-01-06. The suggested
replacement, `gpt-realtime-2.1-mini`, requires the Realtime API and does not work
at `/v1/audio/speech`; that workflow is outside this router's implemented adapter.
The documented built-in voices are `alloy`, `ash`, `ballad`, `coral`, `echo`,
`fable`, `nova`, `onyx`, `sage`, `shimmer`, `verse`, `marin`, `cedar`.
`marin`/`cedar` are recommended; voices are optimized for English. Older TTS
models have a smaller voice subset. Multilingual TTS includes English, Hindi,
French, German, Spanish, Japanese and Chinese; use original-language text.
Source: [OpenAI TTS guide](https://developers.openai.com/api/docs/guides/text-to-speech).
The API reference also lists the pinned TTS ID `gpt-4o-mini-tts-2025-12-15`:
[create speech](https://developers.openai.com/api/reference/resources/audio/subresources/speech/methods/create).

Deepgram recommends `nova-3` for general recorded speech; `nova-2` remains an
alternative where its language coverage is needed. `nova-3` supports the proposed
English, Hindi (`hi`), French (`fr`), German (`de`), Spanish (`es`), Japanese
(`ja`) and Mandarin (`zh`) tests. Explicitly set the language: default is `en`.
`language=multi` is a separate code-switching test. Flux models are streaming
models and unsuitable for this router's recorded-audio adapter.
Sources: [model/language table](https://developers.deepgram.com/docs/models-languages-overview),
[Nova-3 multilingual languages](https://developers.deepgram.com/docs/nova-3-language-prompting).

Deepgram voice IDs include the language: `aura-2-thalia-en`,
`aura-2-apollo-en`, `aura-2-andromeda-en` are public examples. Aura currently
lists English, Spanish, German, French, Dutch, Italian and Japanese. Hindi is
not in that TTS language list. Use the provider's exact voice-model ID for each
language, rather than changing an English voice's language field.
Source: [Aura voices/languages](https://developers.deepgram.com/docs/tts-models).

ElevenLabs now lists `scribe_v3` for recorded STT, with `scribe_v2` as its
predecessor. TTS includes `eleven_v4`, `eleven_v4_turbo`, `eleven_v3`,
`eleven_multilingual_v2`, `eleven_flash_v2_5`, `eleven_flash_v2`.
Use the established multilingual-v2 default above; add v4 only after discovery
and a successful probe. Multilingual-v2 and multilingual-STS-v2 both list
English, Hindi, French, German, Spanish, Japanese and Chinese among their
29 languages; Scribe lists 90+ languages. `eleven_english_sts_v2` is English-only.
Source: [ElevenLabs models and language table](https://elevenlabs.io/docs/overview/models).

ElevenLabs S2S is voice conversion, preserving delivery and spoken content;
it is not a speech translation test. The current endpoint is
`/v1/voice-changer/{voice_id}`, multipart `audio` plus `model_id`.
Its public example uses voice ID `JBFqnCBsd6RMkjVDRZzb`; treat this as a
candidate, never an entitlement guarantee.
Source: [voice changer API](https://elevenlabs.io/docs/api-reference/speech-to-speech/convert).

## What needs authenticated discovery

- Deepgram: request authenticated `/v1/models` for public metadata, exact
  canonical IDs, languages and `batch` support. Custom/project models need
  `/v1/projects/{project_id}/models`. A public listing does not establish the
  account's balance, permissions or successful inference.
  Sources: [public models](https://developers.deepgram.com/reference/manage/models/list),
  [project models](https://developers.deepgram.com/reference/manage/projects/models/list).
- ElevenLabs: use `/v1/models` capability flags
  `can_do_text_to_speech`/`can_do_voice_conversion`, language metadata,
  `requires_alpha_access` and pro-voice compatibility. Public model metadata
  alone does not prove account access.
  Source: [model discovery](https://elevenlabs.io/docs/api-reference/models/list).
  Page authenticated `/v2/voices` with `xi-api-key` until `has_more=false`;
  select an available default voice or a voice the account already owns.
  Voice names, community visibility and a copied public ID do not prove
  permission to synthesize. Record actual ID, labels and model compatibility.
  Source: [user voice discovery](https://elevenlabs.io/docs/api-reference/voices/search).
- OpenAI: built-in TTS voice names are public. Project access, limits and custom
  voice permissions remain unknown until authenticated checks and inference.
  This repo manually registers OpenAI audio IDs; generic catalog visibility is
  not an inference probe. See [adapter coverage](adapters.md).

## Existing local TTS weights: test these first

Local inspection reported a MacBook Air M4 with 24 GB unified RAM and cached
weight files for `mlx-community/Qwen3-TTS-12Hz-0.6B-CustomVoice-8bit`
(about 1.29 GB safetensors) and `mlx-community/chatterbox-turbo-fp16`
(about 2.99 GB safetensors). Prefer these to downloading Kokoro. Directory and
weight-file presence does not prove complete dependencies, compatible serving,
successful inference or acceptable latency.

The original [Qwen 0.6B CustomVoice card](https://huggingface.co/Qwen/Qwen3-TTS-12Hz-0.6B-CustomVoice)
declares Apache-2.0 and ten languages: Chinese, English, Japanese, Korean,
German, French, Russian, Portuguese, Spanish and Italian. Hindi is absent.
Its speaker IDs are `Vivian`, `Serena`, `Uncle_Fu`, `Dylan`, `Eric`, `Ryan`,
`Aiden`, `Ono_Anna`, `Sohee`. Start with English/`Ryan`; add French/`Ryan`
as a multilingual check and Japanese/`Ono_Anna` for a native speaker test.
These are upstream speaker IDs; verify the local runtime's spelling/case and
request mapping. The [MLX conversion card](https://huggingface.co/mlx-community/Qwen3-TTS-12Hz-0.6B-CustomVoice-8bit)
identifies the same original checkpoint and Apache-2.0 metadata; its example
uses `mlx-audio`. Neither card provides an HTTP service by itself.

The original [Resemble Chatterbox Turbo card](https://huggingface.co/ResembleAI/chatterbox-turbo)
declares MIT and English-only TTS. Its multilingual sibling is a different
model; do not declare Hindi or French on Turbo. The original example uses a
reference audio clip, rather than enumerated built-in voice IDs.
The [MLX Turbo conversion card](https://huggingface.co/mlx-community/chatterbox-turbo-fp16)
also documents generation with a default voice and optional reference audio.
Use its default voice for the first English smoke test, with the actual local
wrapper's voice identifier; no public named voice ID is established by these
cards. The conversion metadata says Apache-2.0 while the original says MIT:
retain and inspect the upstream notices and downloaded license files before
redistribution instead of treating conversion metadata as a replacement license.
Both cached candidates are TTS; reference-audio conditioning does not make them
native S2S translation models.

## Additional public Hugging Face candidates

| Repository | License | Native task and concrete smoke test |
| --- | --- | --- |
| `Systran/faster-whisper-large-v3` | MIT | CTranslate2 Whisper STT; one English clip, then Hindi/French; no voice or S2S output |
| `hexgrad/Kokoro-82M` | Apache-2.0 weights | TTS; English `af_heart`, French `ff_siwis`, Hindi `hf_alpha` |
| `facebook/seamless-m4t-v2-large` | CC-BY-NC-4.0 | Native speech translation; English audio → French speech (`tgt_lang=fra`), then Hindi (`hin`) |

The [Systran model card](https://huggingface.co/Systran/faster-whisper-large-v3)
identifies this as a conversion of Whisper large-v3, with multilingual ASR.
The [Kokoro card](https://huggingface.co/hexgrad/Kokoro-82M) documents 24 kHz
output in its example; its [voice table](https://huggingface.co/hexgrad/Kokoro-82M/blob/main/VOICES.md)
covers American/British English, Japanese, Mandarin, Spanish, French, Hindi,
Italian and Brazilian Portuguese. Kokoro has no native audio-input S2S task.

The [Meta model card](https://huggingface.co/facebook/seamless-m4t-v2-large)
documents native S2ST, ASR and other translation tasks: 101 speech-input
languages versus 35 speech-output languages. Its audio example resamples input
to 16 kHz before one `model.generate` call. `eng`, `fra`, `hin`, `deu`, `spa`,
`jpn`, `cmn` support speech output. Check the target column rather than assuming
all input languages can produce audio. This is an integrated model with internal
translation stages, not an external STT/LLM/TTS orchestration. Its noncommercial
weight license means it is not a default commercial deployment candidate.

All three are weights/libraries, not a running OpenAI-compatible HTTP service.
Local tests require an existing serving wrapper implementing this repo's
STT/TTS or native `/audio/speech-to-speech` contract, correct model IDs, language
mapping, output formats and MIME types. Runtime availability, memory, latency,
wrapper compatibility and actual output quality are unverified.

## Public test audio and references

| Source | License and references | Small proposed selection |
| --- | --- | --- |
| [LibriSpeech / OpenSLR 12](https://www.openslr.org/12) | CC BY 4.0; English read speech; reference transcripts accompany the corpus | 3 short `test-clean` clips from different speakers, then 1 `test-other` clip |
| [Google FLEURS](https://huggingface.co/datasets/google/fleurs) | CC BY 4.0; audio, normalized `transcription`, `raw_transcription`, language metadata; parallel sentences across languages | 2 test clips each from `en_us`, `hi_in`, `fr_fr`, `de_de`, `es_419`, `ja_jp`, `cmn_hans_cn` |

Do not fetch full archives just for a smoke test: OpenSLR's `test-clean` archive
is about 346 MB. Preserve attribution, original clip ID, dataset revision,
language/config/split, transcript, source URL, SHA-256 and duration in the
fixture manifest. Use original clips for STT; use short original-language text
for TTS. Dataset text can test TTS content without implying there is one correct
reference waveform.

FLEURS provides parallel content, but row number, audio filename and speaker are
not a translation alignment guarantee. Confirm sentence identity against
parallel metadata before treating another language's transcript as a translation
reference. Its card calls `id` the audio sample ID. For initial native S2S tests,
human-check translated meaning against a verified target sentence; never score
translation as if voice conversion should change language.

## Minimal run and evidence

1. Discover accessible models/voices; register their real IDs and narrow
   deployment capabilities. Keep credentials out of manifests and artifacts.
2. Run one short English request for each cloud row; then the supported
   Hindi/French variants. For local candidates run only when a server exists.
3. Validate STT JSON/text and compare normalized transcript words; save WER and
   normalization rules. Validate generated audio decodes, has nonzero duration
   and correct content type; listen for intelligibility and correct language.
4. Pin every baseline to an explicit `deployment:<id>`. Do not switch models or
   providers on failure. Record the actual failure and its activity record; no
   substitute output or fallback counts as a passed model test.

Unknown until execution: account entitlements, current credits, exact per-model
language quality, actual voice compatibility, endpoint success, latency, cost
and local hardware fit. No paid calls, large downloads or model executions were
performed for this note. Public aliases and catalogs can change after this date.


## Local inspection and acquired fixtures

Read-only inspection on 2026-11-17 found an Apple M4 MacBook Air with 24 GB
unified memory. Weight files are present in the Hugging Face cache for
`mlx-community/Qwen3-TTS-12Hz-0.6B-CustomVoice-8bit` (about 1.29 GB) and
`mlx-community/chatterbox-turbo-fp16` (about 2.99 GB). Cached weight presence is
not proof that all auxiliary assets are available or that inference works.
The default `python3` environment did not contain mlx-audio, MLX, Transformers,
PyTorch, faster-whisper or soundfile. This does not inventory other virtualenvs.
No separate Python speech-serving process was observed among current TCP
listeners; the existing Voice Cloud Docker services are running. No service was
restarted or configured by this research.

Two public LibriSpeech clean-test FLAC files are now under
[`testdata/audio`](../testdata/audio/README.md), with dataset IDs, original
reference transcripts, byte counts and SHA-256 hashes in `manifest.json`.
They are real human recordings, not synthesized stand-ins. Both current clips
have the same speaker ID; this is a small English baseline, not representative
multispeaker/noise/language coverage. The larger clip requires the direct API
because it exceeds the playground's 256 KiB upload limit.

No inference, paid calls, or server setup was performed. API keys, account
entitlements and a paid-test budget cannot be established by public web research.
Public clips can support baseline testing; application-specific acceptance
criteria and private production data require the owner's context.
