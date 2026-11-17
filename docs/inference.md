# Inference API

The router exposes three OpenAI-compatible HTTP endpoints:

| Path | Task | Request body |
| --- | --- | --- |
| `POST /v1/chat/completions` | LLM | JSON with `messages` |
| `POST /v1/audio/speech` | TTS | JSON with `input` and `voice` |
| `POST /v1/audio/transcriptions` | STT | Multipart form with an audio `file` |

Use a router key minted in the console or through `POST /v1/api-keys` as
`Authorization: Bearer vc_...`. Expired or revoked keys are rejected. User
sessions and `ADMIN_TOKEN` cannot authenticate inference requests; router keys
cannot authenticate management requests. Keys share access to the installation's
routes; a default policy is a convenience, not an access restriction.

## Choose a route

The request's `model` selects a router resource:

- `policy:1` uses routing policy 1, including its fallback settings.
- `deployment:1` sends directly to deployment 1 without fallback.
- Omit `model` to use the router key's default policy. A key without a default
  requires an explicit selector.

Replace example IDs with your configured resource IDs. These selectors are not
provider model names: the router substitutes each deployment's
`upstream_model_id` before sending the request. The route's task must match the
endpoint. Deployment and connection must both be enabled.

```sh
export ROUTER_KEY='vc_...'

curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer $ROUTER_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"policy:1","messages":[{"role":"user","content":"Say hello."}]}'

curl http://localhost:8080/v1/audio/speech \
  -H "Authorization: Bearer $ROUTER_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"policy:2","input":"Hello from Voice Cloud.","voice":"alloy"}' \
  --output speech.mp3

curl http://localhost:8080/v1/audio/transcriptions \
  -H "Authorization: Bearer $ROUTER_KEY" \
  -F model=policy:3 -F file=@speech.mp3 -F language=en
```

Provider options pass through using the selected endpoint's request format. The
provider still determines which models, voices, formats, and options it supports;
see the [OpenAI audio guide](https://developers.openai.com/api/docs/guides/audio)
for the compatible audio formats. The router does not translate native provider
protocols or convert audio.

## Connections and credentials

Supported adapter identifiers are `openai`, `openai-compatible`, `local`,
`custom`, and `whisper`. Each uses the OpenAI-compatible HTTP protocol. In
particular, naming an adapter `local` or `whisper` does not adapt an arbitrary
server's native API. Other identifiers can be saved in configuration but cannot
serve inference yet.

Set a connection's base URL to the API root, for example
`https://api.openai.com` or `http://localhost:9000/v1`. The router appends `/v1`
unless the path already ends in `/v1`, then appends `/chat/completions`,
`/audio/speech`, or `/audio/transcriptions`.

`credential_ref` names an environment variable in the **API process**, for
example `OPENAI_API_KEY`. The router reads its value and uses it as the upstream
bearer credential. A configured reference with no value fails the attempt;
an absent reference sends no upstream Authorization header. Client router keys
and other client headers are not forwarded as provider credentials. Redirects
are not followed. See [Docker configuration](docker.md#provider-credentials)
for passing a secret to the backend container.

## Policies and fallback

Targets are eligible when their connection and deployment are enabled, their
task matches, and policy constraints permit them. A fresh unhealthy worker
result excludes a deployment; missing, stale, or changed-configuration health is
unknown and does not by itself exclude it. Reachability checks are not inference
benchmarks; see [worker health](worker.md).

When `language` is supplied, deployment language tags filter targets.
Deployments without language tags accept any language. Matching is case
insensitive and accepts a matching base language, such as `en` and `en-US`.

Supported `constraints_json` fields are:

```json
{"location_kind":"local","max_latency_ms":1000,"max_estimated_attempt_cost_nanos":1000000}
```

`location_kind` accepts `local` or `cloud`. `max_latency_ms` requires a recent
successful inference first-output measurement within the limit. The router uses
the median from up to 20 completed attempts in the last ten minutes, ignoring
attempts from before the deployment or connection was edited. A generic GET health
probe is not an inference latency sample. Cold deployments without measurements
are excluded only when this latency constraint is set. This is a historical
selection filter, not a guarantee on the next request; use first-output deadlines
to bound that request. Unsupported or invalid constraints fail routing.

`max_estimated_attempt_cost_nanos` filters each generation attempt by its
configured upper estimate. Unknown pricing or unbounded quantities exclude a
target when this constraint is set. For text LLMs, send `max_tokens` or
`max_completion_tokens`; input bytes plus framing conservatively bound input
tokens. TTS can use per-character rates; STT requires a configured flat request
price because the router cannot know audio duration before transcription.
This cap applies per generation attempt, excludes decision-service charges, and
is based on your configured prices. Fallback can incur more than one attempt;
it is not a total invoice ceiling. Provider-side spending limits are still needed.

Three consecutive transient failures start a 30-second deployment cooldown.
Timeouts, network failures, malformed responses, HTTP 408/429/5xx, and interrupted
streams count; client cancellations and configuration errors do not. After the
cooldown expires, the deployment can receive traffic again. The state comes from
saved attempts and survives API restarts.

Without decision assistance, target priority is primary, with lower values first. Within equal priorities,
`latency` prefers fresh lower measured latency; `cost` prefers lower configured
`pricing_json` estimates for the request. Flat request prices are directly comparable; text-LLM unit-rate estimates require an explicit output-token bound, and TTS estimates use the input character count. Audio-input cost without a known duration remains unknown. Unknown measurements or prices rank last.
`balanced` and `quality` follow target priority and deployment ID; quality is
configured through priorities, not inferred by the router.

With fallback enabled, the router tries at most `max_attempts` eligible targets,
once each. Unavailable credentials, unsupported adapters, invalid connection
configuration, network failures, and upstream HTTP 401, 403, 404, 408, 429, or 5xx responses
allow fallback. Other upstream failures stop the request. Disabling fallback
limits the request to one attempt. Provider credential/account/model failures can therefore use another target; client-shape errors such as HTTP 400 stop the request. A failed attempt may still incur provider charges.

## Request compatibility

Eligibility checks run before any decision-service or generation call. Declare
supported features in each deployment's `capabilities_json`, for example:

```json
{
  "streaming": true,
  "tools": true,
  "vision": false,
  "max_input_bytes": 32768,
  "max_output_tokens": 4096,
  "routing_description": "Our verified fast chat deployment"
}
```

Advanced features require an explicit `true`: `streaming`, `tools`, `vision`,
`audio_input`, `audio_output`, `json_output`, and `json_schema`. Output limits
requested with `max_tokens` or `max_completion_tokens` must fit the declared
`max_output_tokens`. Optional positive `max_input_bytes`, `max_input_characters`,
and `context_window` bounds also filter candidates. Context checks conservatively
use serialized input bytes plus framing as an upper token budget; they are not a
provider tokenizer. Plain LLM requests above 4,096 serialized bytes need a declared
input or context bound. Accurate capabilities remain the operator's responsibility.

TTS requires explicit matching `voices` and `response_formats` lists. For example,
`{"voices":["alloy"],"response_formats":["mp3"]}` permits that combination.
Non-default STT formats also require a matching `response_formats` entry; default
JSON transcription needs no format list. Voice names are not translated across
providers. Declare only formats and voices you have verified on that deployment.

If no policy target fits, HTTP 503 includes safe rejection counts, such as
`capability_tools:1` or `unsupported_voice:2`. An incompatible explicitly selected
deployment returns HTTP 400. Unsupported native adapters are excluded. Use the
[console playground and first-route guide](quickstart.md) to test configuration.

## Decision-assisted LLM routing

An LLM policy can optionally ask a decision model to choose its first deployment
for each request. The application keeps sending `model: "policy:<id>"`; no client
change is needed. Empty `decision_json` (`{}`), including its default on existing
policies, disables this behavior. STT and TTS continue to use deterministic routing.

Configure the policy's **Decision model** field in the console, or send
`decision_json` through the management API:

```json
{
  "url": "https://api.typesafe.ai/v1/systemone",
  "model": "jev-latest",
  "credential_ref": "TYPESAFE_API_KEY",
  "timeout_ms": 500,
  "min_confidence": 0.6
}
```

This uses the [TypeSafe System One Choice protocol](https://docs.typesafe.ai/primitives/choice):
the router supplies eligible deployment options, and the service returns a choice
and confidence. Jev is supported through this protocol; another decision service
must implement the same wire format. A general OpenAI chat endpoint is not a
compatible decision endpoint.

`url` is the complete HTTP(S) endpoint, without user credentials, a query, or a
fragment. `model` selects the decision model. `credential_ref` optionally names an
API-process environment variable whose value becomes the decision service's
bearer token. An absent reference sends no token, which can be useful for a local
decision endpoint. A configured reference must resolve to a value. Never put a
secret in the JSON. The [Docker credential override](docker.md#provider-credentials)
can pass `TYPESAFE_API_KEY` to the API process. `timeout_ms` defaults to 500 and
accepts 50–5,000; `min_confidence` defaults to 0.6 and accepts 0–1.

Enabling this policy sends plain-text message roles and content to the configured
decision service, including system prompts and any sensitive text in those
messages, in addition to the generation provider. Other message fields are not
sent. The decision step adds latency and may incur a separate charge. Requests
with tools/functions, tool calls, or non-string message content bypass it; images,
audio, and tool arguments are never sent to the decision service. Text context
larger than 16 KiB after serialization also uses the deterministic route instead.
The entire decision payload is capped at 64 KiB, with at most 255 candidates;
candidate names and upstream model identifiers must each fit in 500 UTF-8 bytes.
Exceeding these limits also keeps the deterministic route.
Choose the endpoint according to your application's data requirements.

Every eligible deployment must have a nonblank `routing_description` in its
**Capabilities**, otherwise the request keeps its deterministic route. For a fast
general-purpose deployment, an operator description might be:

```json
{"routing_description":"Small local model for short English FAQ answers; not evaluated for complex reasoning."}
```

For a deployment evaluated for more demanding reasoning:

```json
{"routing_description":"Larger reasoning model evaluated on our multi-step technical questions; use for those tasks when the extra latency and cost are acceptable."}
```

Adapt these descriptions to your own results. Each `routing_description` can
contain up to 2,000 UTF-8 bytes. The decision receives
these descriptions, model identifiers, and configured pricing information. It does
not invent benchmark results, train a router, or establish which model is best.
Use descriptions supported by your own evaluations, then compare the policy's
cost, latency, and answer quality against a fixed-deployment baseline.

Hard task, language, health, and policy constraints filter candidates first. The
decision can choose any remaining target, even one with a lower configured
priority. That deployment moves first; the other targets retain their original
deterministic order for fallback. `max_attempts` and `fallback_enabled` still
control generation attempts. Explicit `deployment:<id>` requests and policies
with only one eligible deployment bypass the decision call.

Timeouts, low confidence, unavailable credentials, and invalid decisions keep the
deterministic order. A returned choice cannot introduce a deployment outside the
eligible set. The router records decision metadata in the request's routing
snapshot, and the attempt selection reason identifies decision-assisted routing.
Metadata includes status, configured and returned model identifiers, chosen
deployment, confidence, decision latency, and token usage when reported. It stores
no prompt text. Decision-service usage and estimated cost, when configured, are recorded separately from generation attempts; the overview includes them in its known partial total.

For an existing LLM policy, read its current fields before replacing it. For
example, if policy 1 has the following editable values:

```sh
curl -X PUT http://localhost:8080/v1/policies/1 \
  -H "Authorization: Bearer $SESSION_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"App chat","task":"llm","objective":"balanced","fallback_enabled":true,"max_attempts":2,"constraints_json":{},"decision_json":{"url":"https://api.typesafe.ai/v1/systemone","model":"jev-latest","credential_ref":"TYPESAFE_API_KEY","timeout_ms":500,"min_confidence":0.6}}'
```

Use the policy's actual ID and preserve its existing name, objective, constraints,
and fallback settings. PUT replaces editable fields; omitting `decision_json`
resets it to `{}`. To disable the decision call, save `{}` in that field.

## Streaming and limits

Compatible streaming responses pass through and flush incrementally. For chat,
use `"stream":true`; request `"stream_options":{"include_usage":true}` when the
provider supports it to receive token usage in the stream. Audio responses pass
through as bytes. The router validates ordinary JSON before returning it and waits for the first usable stream event or audio bytes before committing a response. Invalid responses and first-output timeouts can use fallback. There is no fallback after output starts: a broken stream
ends and is recorded as failed or cancelled rather than being restarted.

Request bodies, including multipart overhead, are limited to 32 MiB. Multipart
uploads above 1 MiB spill to temporary files that are removed after the request.
Non-stream JSON/text responses are limited to 8 MiB, audio responses to 128 MiB,
and individual SSE events to 1 MiB. Requests have a five-minute total timeout.
Each deployment defaults to 30 seconds for first output and 15 seconds between
chunks. Override these limits in `settings_json` using
`first_output_timeout_ms` and `idle_timeout_ms`, each an integer from 50 to 120000.
The first-output deadline includes connecting to the provider. Streams that send
only comments or metadata cannot keep the first-output deadline alive indefinitely.
An SSE completion marker closes the upstream stream without waiting for HTTP EOF.
 Reverse proxies must allow the same upload size and
duration and disable response buffering for streaming. WebSocket/realtime
sessions and native provider protocols are not implemented.

The generated `X-Request-ID` identifies the activity record. An optional
`Idempotency-Key` claims a key per router API key before dispatch. Reusing it
returns HTTP 409, whether or not the body matches and whether or not the first
request has finished. Responses and streams are never replayed. Use a new key
for an intentional new request. Claims remain until their request history is
removed by the [retention policy](operations.md#interrupted-requests-and-retention);
reusing a key after retention can trigger a new provider call.

## Activity and cost

Requests and provider attempts populate the console's Activity and Logs pages
and the management API's `/v1/requests` reads. Records include status, timing,
selected deployment, safe error codes, provider request ID when available, and
usage. Prompt text and audio are not stored in SQLite.

Usage includes requests, TTS input characters, provider-reported audio duration,
and provider token counts from compatible JSON or stream events. Missing usage
remains unknown rather than being inferred from output bytes.

Deployment `pricing_json` accepts nonnegative integer nanodollar rates:

```json
{
  "input_token_nanos": 1000,
  "output_token_nanos": 3000,
  "audio_ms_nanos": 100,
  "input_character_nanos": 15000
}
```

Configure only the rates relevant to that deployment. Quantities are multiplied
by the corresponding rate with overflow checks; an unknown or overflowing cost
remains unknown. A nanodollar is 10^-9 USD. Rates come from your configuration,
not a synchronized provider catalog. For example, $1 per million input tokens
is `input_token_nanos:1000`. Do not copy example rates as vendor prices.

Alternatively, `{"request_nanos":1000000}` estimates a flat $0.001 for a successful
attempt. This flat price covers the whole attempt; its other usage rows have zero
additional cost to prevent double counting. Failed attempts can still be charged
by providers: unreported charges stay unknown. These are estimates, not invoices.

A decision policy may additionally supply
`"pricing":{"input_token_nanos":42,"output_token_nanos":42}` inside
`decision_json`. Both rates and reported token counts are required to estimate
the decision charge. Decision metadata includes whether a call was attempted,
reported usage, and estimated or unknown cost. `/v1/analytics` and the overview
include known decision costs and explicitly count unknown decisions and attempts.
The known sum is **partial** whenever any cost is unknown. Provider billing
reconciliation remains outside this router.

## Traffic controls

The API admits work before reading upload bodies. Defaults are intentionally
small for one self-hosted API process:

| API environment variable | Default | Meaning |
| --- | --- | --- |
| `INFERENCE_MAX_CONCURRENCY` | 2 | Active inference requests across the process |
| `INFERENCE_PER_KEY_CONCURRENCY` | 2 | Active requests from one router key |
| `INFERENCE_REQUESTS_PER_MINUTE` | 60 | Requests admitted per key per minute |
| `INFERENCE_DAILY_REQUEST_LIMIT` | 10000 | Per-key saved requests per UTC day |

Values must be integers from 1 to 1000000; startup rejects invalid settings.
Excess traffic gets HTTP 429 and `Retry-After:60`. Minute rate and concurrency
state is process-local; daily quotas consult saved requests and survive API
restarts. In-flight uploads are counted conservatively against the daily limit.
Invalid uploads can consume minute-rate allowance without producing a saved record.

These controls limit requests, **not provider invoices**. Configure provider-side
spending limits too; unknown prices, failed attempts, and independent provider
charges prevent a router from guaranteeing a dollar spending ceiling. Multiple
API replicas do not share these process-local limits; the supported deployment is
one API process and one maintenance worker.
