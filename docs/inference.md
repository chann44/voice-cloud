# Speech inference API

Voice Cloud serves exactly three tasks:

| Endpoint | Task | Input | Output |
| --- | --- | --- | --- |
| `POST /v1/audio/speech` | TTS | JSON `input`, `voice`, optional `response_format` | Audio |
| `POST /v1/audio/transcriptions` | STT | Multipart `file`, optional `language`/`response_format` | Transcript |
| `POST /v1/audio/speech-to-speech` | S2S | Multipart `file`, optional `voice`/`target_language`/`response_format` | Audio |

Authenticate using `Authorization: Bearer vc_...` with a router key from the
console. User sessions and `ADMIN_TOKEN` are management credentials and cannot
call inference. Provider keys are never client router keys; the API resolves them
from [encrypted storage](secrets.md) or a configured environment reference.

`model: "policy:<id>"` selects routing/fallback; `deployment:<id>` selects one
deployment directly. Omit `model` only when the router key has a default policy.
These selectors are replaced with the actual upstream model ID. The task must
match the endpoint. There is no chat API: `/v1/chat/completions` returns HTTP 410.
Creating/importing an `llm` model or policy is rejected.

## Providers and native models

See [adapter coverage](adapters.md) for supported providers, native S2S, voice IDs,
formats and the local model-server contract. Configure capabilities explicitly:

```json
{"max_input_bytes":8388608,"voices":["your-voice-id"],"response_formats":["mp3"]}
```

TTS needs matching voices and formats. S2S needs matching output formats and,
when a voice is requested, matching voices. Native translation requests require a
matching declared `target_languages` entry. Nondefault STT output formats require
matching `response_formats`. Transcript SSE needs `streaming:true`; speech output
uses audio bytes, not SSE. The router does not convert audio or start model servers.

Stored keys take precedence over environment credentials. Missing or undecryptable
credentials fail closed. Provider request redirects are refused. Native adapters
use the appropriate `Token`, `api-key`, or `xi-api-key` headers; compatible speech
servers normally use bearer authentication. Secrets are not returned in API reads.

## Routing and failover

Connections and deployments must be enabled. Hard task, capability and language
filters run before dispatch. Language tags accept matching base languages. Fresh
unhealthy worker probes exclude targets; unknown/stale reachability is not proof
of failed inference. See [worker health](worker.md).

Lower target priority runs first. Within equal priority, `cost` prefers a known
configured request estimate; `latency` prefers recent successful median time to
first output (up to 20 attempts in ten minutes after the last configuration edit).
`balanced`/`quality` use configured priority and deployment ID. The router does
not infer model quality or call a decision model.

Supported policy constraints:

```json
{"location_kind":"local","max_latency_ms":1000,"max_estimated_attempt_cost_nanos":1000000}
```

Unknown latency excludes a target only when a latency bound is requested. A cost
bound excludes unknown estimates; TTS can estimate by input characters, while
STT/S2S require a flat request price because audio duration is unknown before
inference. The bound is per attempt, based on configured prices, not an invoice
ceiling. Fallback attempts may each incur charges; use upstream spending limits.

Fallback tries at most `max_attempts` distinct eligible targets. It can retry
network/configuration failures, malformed success responses, first-output timeouts,
and HTTP 401/403/404/408/429/5xx. Other upstream 4xx stop the request. There is no
fallback after audio/transcript output begins; interrupted streams are recorded
as failures. Three consecutive transient failures create a durable 30-second
cooldown. Incompatible direct deployments return 400; an empty policy returns
503 with safe rejection counts.

## Limits and accounting

Requests are limited to 32 MiB including multipart overhead. Uploads above 1 MiB
spill to temporary files and are removed afterward. JSON/text responses are
limited to 8 MiB, audio to 128 MiB and SSE events to 1 MiB. Requests time out after
five minutes. Deployment `settings_json` accepts `first_output_timeout_ms`
(default 30000) and `idle_timeout_ms` (default 15000), each 50–120000. Streaming
proxies must disable buffering and allow the same request size/duration.

`X-Request-ID` links to Activity, attempts and usage. `Idempotency-Key` claims a
key per router API key; any reuse returns 409. Responses/streams are not replayed.
History and claims expire together under [retention](operations.md).

Pricing uses nonnegative integer nanodollars per unit (`request_nanos`,
`audio_ms_nanos`, `input_character_nanos`, and provider-reported input/output token
rates). Unreported quantities and missing/overflowing rates remain unknown.
Known dashboard totals are partial whenever any attempt cost is unknown.
Historical records are retained across the speech-only upgrade.

| API environment variable | Default | Meaning |
| --- | --- | --- |
| `INFERENCE_MAX_CONCURRENCY` | 2 | Active requests across one API process |
| `INFERENCE_PER_KEY_CONCURRENCY` | 2 | Active requests per router key |
| `INFERENCE_REQUESTS_PER_MINUTE` | 60 | Per-key minute admissions |
| `INFERENCE_DAILY_REQUEST_LIMIT` | 10000 | Per-key durable UTC daily quota |

Values must be 1–1000000. Excess work receives 429 and `Retry-After:60` before
upload processing. Minute/concurrency state is process-local; daily quota is
persistent. The supported topology is one API process and one maintenance worker.
