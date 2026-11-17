# Speech adapter coverage

Voice Cloud supports only **speech-to-text (`stt`), text-to-speech (`tts`), and
native speech-to-speech (`s2s`)**. It does not serve chat completions, text-only
LLMs, or STT → LLM → TTS pipelines. Existing text-model records are preserved for
history, but are excluded from active catalogs and cannot serve new requests.

The authenticated `GET /v1/adapters` and **Providers → Implemented adapters** show
the same ten implemented adapters:

| Adapter | Tasks | Model setup |
| --- | --- | --- |
| `openai` | STT, TTS | Enter an actual audio model ID manually |
| `azure` | STT, TTS | Azure OpenAI v1 resource URL; enter the deployed audio model's deployment name |
| `deepinfra` | STT, TTS | Compatible audio endpoints under `/v1/openai`; manual model registration |
| `together` | STT, TTS | Compatible audio endpoints; manual audio model registration |
| `deepgram` | STT, TTS | Native recorded-audio HTTP APIs; live speech catalog |
| `elevenlabs` | STT, TTS, S2S | Native recorded-audio HTTP APIs; TTS/voice-conversion catalog, manual Scribe registration |
| `whisper` | STT | OpenAI-compatible transcription server |
| `local`, `custom`, `openai-compatible` | STT, TTS, S2S | Your speech serving process; native S2S uses the contract below |

Task support describes an adapter, not every model from a provider. Declare each
deployment's actual input limits, voices and response formats. Provider keys use
[encrypted server storage](secrets.md). Other provider templates are not runnable
until an appropriate speech protocol adapter exists. Text-only providers such as
Anthropic and Cerebras are not offered as supported adapters.

## Native speech-to-speech

`POST /v1/audio/speech-to-speech` accepts a multipart `file` containing input audio,
`model` selecting a policy/deployment, optional `voice`, `response_format`
(default `mp3`), and optional `target_language`. It returns audio bytes.

This sends audio directly to one native model endpoint. It never assembles a
transcription/chat/synthesis pipeline. Upload limits, timeouts, pre-output failover,
activity records, API-key quotas and secret handling are shared with STT/TTS.
There is no failover after audio output begins.

- **ElevenLabs voice conversion:** maps to the current
  [`/v1/voice-changer/<voice_id>` API](https://elevenlabs.io/docs/api-reference/speech-to-speech/convert),
  using `xi-api-key`, multipart `audio` and `model_id`, and MP3 output. Use a real
  voice ID and a model advertising `can_do_voice_conversion`. Declare matching
  `voices` and `response_formats` on the deployment. Voice conversion preserves
  the spoken content/language and rejects translation requests.
- **Local/custom translation or conversion:** your serving process must expose
  `POST /v1/audio/speech-to-speech` with multipart `file`, the real upstream model
  ID in `model`, and the requested voice/format/language options. It must return
  audio with the correct `audio/*` MIME type. This is Voice Cloud's upload contract,
  not a standard OpenAI endpoint. A Hugging Face model needs a running HTTP wrapper.
  No built-in cloud translation adapter or model hosting is implied.

Translation deployments must declare the exact supported `target_languages`, for
example `{"response_formats":["mp3"],"target_languages":["fr","de"]}`. A requested
unsupported target is excluded before a provider call. A voice is optional for
native translation models; if supplied, it must match declared `voices`.
SSE and WebSocket/realtime conversation sessions are not supported for S2S.

## STT and TTS

The public routes remain `POST /v1/audio/transcriptions` (multipart audio) and
`POST /v1/audio/speech` (JSON `input`, `voice`). Speech output streams as audio
bytes. Compatible STT models may return transcript SSE when explicitly declared.

Deepgram uses `Authorization: Token ...`, raw audio upload to `/v1/listen`, and
JSON `text` to `/v1/speak`. STT supports JSON/text; TTS supports MP3/WAV and speed
0.7–1.5. The Aura model ID includes the voice: use that ID as both the deployment's
upstream model and the request's declared voice. Native transcription responses
are normalized to `text` and duration when reported.

ElevenLabs uses `xi-api-key`, multipart `/v1/speech-to-text` for STT and
`/v1/text-to-speech/<voice_id>` for TTS. STT supports JSON/text and manual Scribe
model IDs. TTS supports MP3 and actual voice IDs; nondefault speed is not
implemented. Unsupported options are rejected before dispatch. Voices are not
translated between providers.

See the official [Deepgram STT](https://developers.deepgram.com/reference/speech-to-text/listen-pre-recorded),
[Deepgram TTS](https://developers.deepgram.com/reference/text-to-speech/speak-request),
[ElevenLabs STT](https://elevenlabs.io/docs/api-reference/speech-to-text/convert),
and [ElevenLabs TTS](https://elevenlabs.io/docs/api-reference/text-to-speech/convert)
references for current model/voice entitlements.

## Model discovery

Deepgram's native catalog excludes realtime-only STT models. ElevenLabs uses
explicit TTS and voice-conversion capability flags. Local/custom catalogs include
only entries explicitly tagged `task: "stt"`, `"tts"`, or `"s2s"`; text and
unclassified entries are omitted. Generic provider model catalogs often omit
modality metadata, so OpenAI/Azure/DeepInfra/Together use manual audio-model
registration. No model name is guessed to be a speech model.

Refreshing discovery never deletes registrations. Model listing is not a real
inference probe; verify each deployment before adding it to a fallback policy.
