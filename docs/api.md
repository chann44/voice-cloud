# Management and speech API

Management calls use an authenticated user session token or configured admin token.
The console handles sessions, CSRF checks and role restrictions. Members can read
configuration; administrators can mutate it. Inference uses separate router keys.
See [accounts](users.md), [speech endpoints](inference.md) and [encrypted provider keys](secrets.md).

## Configuration

`GET/POST /v1/connections`, `/v1/models`, `/v1/deployments`, `/v1/policies` list/create
resources. `GET/PUT/DELETE /v1/<resource>/<id>` reads/replaces/deletes one resource.
PUT replaces editable fields; preserve current values when updating. Referenced
resources cannot be deleted. Model tasks and deployment model/connection/upstream
IDs are immutable; create a new resource when those identities change.

Tasks are **`stt`, `tts`, `s2s` only**. Models require `name`, `publisher`, `task`,
optional `version`. A deployment links `model_id`, `connection_id`, and
`upstream_model_id`, with explicit `capabilities_json`, `pricing_json`, and
`settings_json` objects. See [capabilities and limits](inference.md).

Policies contain `name`, `task`, `objective` (`balanced`, `latency`, `cost`,
`quality`), `fallback_enabled`, `max_attempts`, and `constraints_json`. Defaults
are balanced, fallback enabled, two attempts. Legacy `decision_json` is readable
but only `{}` is accepted on new writes; decision models are unavailable.

`GET /v1/policies/:id/targets` lists targets. `PUT/DELETE
/v1/policies/:id/targets/:deployment` adds/removes one; PUT accepts `{"priority":0}`.
`GET /v1/deployments/:id/languages` lists language tags; `PUT/DELETE
/v1/deployments/:id/languages/:language` maintains them. `GET
/v1/deployments/:id/health` reports reachability, not model-quality benchmarks.

## Providers and credentials

Connections contain `name`, `adapter`, `location_kind` (`local`/`cloud`),
`base_url`, `enabled`, and optional environment `credential_ref`.
POST/PUT accepts write-only `api_key` (up to 4096 bytes; no whitespace/control
characters) and `remove_api_key` (boolean). Blank/omitted keys preserve a saved
key; a nonempty key replaces it and clears the environment reference. Removal
clears both sources. Replacing and removing together is rejected. Keys and
connection changes are committed atomically. Changing a saved key's URL requires
replacing/removing the key. Keys/ciphertext are never returned.

Responses expose `has_api_key`, `secret_storage_available`, `credential_ready`,
`inference_supported`, and `adapter_support` metadata. These indicate configured
storage/protocol support, not verified provider account/model entitlement.

`GET /v1/adapters` lists implemented speech adapters and task/discovery limits.
Administrator-only `POST /v1/connections/:id/discover` fetches the speech catalog;
manual-registration adapters reject discovery explicitly. Results include
`id`, `owned_by`, and a speech `task`. Unknown/text entries are omitted.
`POST /v1/connections/:id/models` atomically registers a model/deployment:

```json
{"upstream_model_id":"actual-speech-model","task":"s2s","capabilities_json":{"voices":["voice-id"],"response_formats":["mp3"]}}
```

It returns 201 new or 200 existing, preserving edits on repeated imports. The
connection must be enabled, have available credentials, and support the task.
This configuration action does not perform paid inference. See [adapter contracts](adapters.md).

## Router keys and activity

`POST /v1/api-keys` accepts `name`, optional `default_policy_id`, and optional
future `expires_at`. It returns the plaintext router key once; store it on your
application server. `GET /v1/api-keys` and `/v1/api-keys/:id` return metadata;
`DELETE /v1/api-keys/:id` revokes it. Router keys cannot call management APIs.

`GET /v1/requests?limit=50`, `/v1/requests/:id`,
`/v1/requests/:id/attempts`, and `/v1/requests/:id/usage` expose activity without
raw prompts/audio or provider credentials. `GET /v1/analytics` provides rolling
24-hour request counts, durations and known/unknown configured cost estimates.
Legacy history is retained; active model/policy catalogs exclude old text tasks.

The only inference endpoints are `/v1/audio/transcriptions`, `/v1/audio/speech`,
and `/v1/audio/speech-to-speech`. The removed chat endpoint returns HTTP 410.
