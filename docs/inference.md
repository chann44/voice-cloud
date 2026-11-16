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
{"location_kind":"local","max_latency_ms":1000}
```

`location_kind` accepts `local` or `cloud`. `max_latency_ms` requires a fresh
healthy latency measurement within the limit. Unsupported or invalid constraints
fail routing rather than being silently ignored.

Without decision assistance, target priority is primary, with lower values first. Within equal priorities,
`latency` prefers fresh lower measured latency; `cost` prefers lower configured
`pricing_json.request_nanos`. Unknown measurements or prices rank last.
`balanced` and `quality` follow target priority and deployment ID; quality is
configured through priorities, not inferred by the router.

With fallback enabled, the router tries at most `max_attempts` eligible targets,
once each. Unavailable credentials, unsupported adapters, invalid connection
configuration, network failures, and upstream HTTP 408, 429, or 5xx responses
allow fallback. Other upstream failures stop the request. Disabling fallback
limits the request to one attempt. A failed attempt may still incur provider charges.

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
no prompt text. Decision-service charges are not included in provider attempt
usage or its cost estimate.

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
through as bytes. There is no fallback after a response starts: a broken stream
ends and is recorded as failed or cancelled rather than being restarted.

Request bodies, including multipart overhead, are limited to 32 MiB. Requests
have a five-minute timeout. Reverse proxies must allow the same upload size and
duration and disable response buffering for streaming. WebSocket/realtime
sessions and native provider protocols are not implemented.

The generated `X-Request-ID` identifies the activity record. An optional
`Idempotency-Key` claims a key per router API key before dispatch. Reusing it
returns HTTP 409, whether or not the body matches and whether or not the first
request has finished. Responses and streams are never replayed. Use a new key
for an intentional new request.

## Activity and cost

Requests and provider attempts populate the console's Activity and Logs pages
and the management API's `/v1/requests` reads. Records include status, timing,
selected deployment, safe error codes, provider request ID when available, and
usage. Prompt text and audio are not stored in SQLite.

Usage includes requests, TTS input characters, provider-reported audio duration,
and provider token counts when reported in compatible JSON or stream events.
Usage inspection is limited to 1 MiB per JSON response or SSE line; larger bodies
still pass through, but their usage may be absent. Missing provider usage remains
unknown; it is not estimated from response bytes. Optional deployment pricing
`{"request_nanos":1000000}` records a fixed estimated price of $0.001 per
successful request. One nanodollar is 10^-9 USD. This is explicit operator
configuration, not a provider price catalog. Token/audio rates and final invoice
reconciliation are not implemented; unknown cost does not mean free.
