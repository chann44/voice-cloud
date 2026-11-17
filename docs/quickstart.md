# First working route

This walkthrough sets up a self-hosted installation and verifies one real chat
request. Start with a single deployment; add another only after both work
individually. Calls to paid providers can incur charges.

## Start the services

From the repository root:

```sh
docker compose -f docker/compose.yml up --build -d
```

Open `http://localhost:3000` and create the first administrator. The API listens
at `http://localhost:8080`. For a provider that needs authentication, pass its key
into the API process using the [Docker credential override](docker.md#provider-credentials).
Keep provider keys out of the browser and the configuration JSON.

## Configure a connection

Open **Providers → Create**. Enter:

- **Name:** a name you recognize, such as `Local runtime` or `OpenAI account`.
- **Adapter:** `openai`, `openai-compatible`, `local`, `custom`, or `whisper`.
- **Location:** `local` or `cloud`.
- **Base URL:** the provider's compatible API root, such as
  `https://api.openai.com` or `http://host.docker.internal:9000/v1`.
- **Credential reference:** the API-process environment variable name, such as
  `OPENAI_API_KEY`. Leave it empty only if that endpoint accepts unauthenticated calls.
- **Enabled:** on.

All five supported adapter identifiers use the OpenAI-compatible HTTP protocol.
They do not translate an arbitrary server's native API. Other seeded catalog
entries are configuration starters and cannot serve native inference yet. A
saved connection or successful generic health probe is not proof that your
account can use the configured model; verify it with a real request.

`localhost` inside Docker refers to the backend container. Use a reachable host
or service name for your model server. The base URL is an API root, not a full
`/chat/completions` path.

## Register the model and deployment

1. Open **Models → Create**. Set the name, publisher, and task `llm`.
2. Open **Deployments → Create**. Choose that model and your connection.
3. Enter the provider's real upstream model ID and enable the deployment.
4. Declare supported input/output bounds in **Capabilities**, for example
   `{"max_input_bytes":4096,"max_output_tokens":128}` for a small test request.
   Use bounds actually supported by the deployment. The console playground
   requests 128 output tokens, so `max_output_tokens` must allow at least 128.
   Declare streaming, tools, or images only after verifying those features.

The model's internal name is a catalog label. The upstream model ID is what the
provider receives. They do not have to match. Voice Cloud does not download or
start a local model server for you.

## Create a policy and key

Open **Routing policies → Create**. Set task `llm`, choose an objective, and save.
Open the policy details and add your deployment as a target with priority `0`.

Open **API keys → Create** and save the router key displayed once. Use this key
for inference. Your console session token is for administration and will not work
at model endpoints. For the following test, the key does not need a default policy
because the route is selected explicitly.

## Verify in the console

Open **Test a route**. Paste the router key, choose the policy or deployment, and
send a short message. The console requests at most 128 output tokens and waits
up to one minute. It uses the key for this request, clears the input afterward,
and does not return or save the raw key in the page data.

Open the resulting **Activity** link to inspect the deployment, attempts, result,
latency, and recorded usage. A policy uses routing and fallback; choosing a
`deployment:<id>` directly isolates that endpoint when diagnosing setup problems.

To verify from your application's backend instead:

```sh
export VOICE_CLOUD_API_KEY='vc_your_router_key'
curl --fail-with-body http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer $VOICE_CLOUD_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"policy:1","messages":[{"role":"user","content":"Reply with a short greeting."}]}'
```

Replace `policy:1` with the actual ID shown in your console. The application
keeps using that policy selector when you change targets.

## Add fallback and speech

Verify a second deployment directly, then add it to the policy with a higher
priority number and enable fallback. Use models compatible with the same request
features. A model that supports text chat may not support tools, images, the same
context length, or the same output options.

Use separate `stt` and `tts` models, deployments, and policies for speech. The
[application examples](../examples/README.md) include file transcription, MP3
speech, and a recorded STT → LLM → TTS turn. TTS voices and output formats must
be declared and supported by each selected deployment. These are HTTP request
flows; bidirectional realtime voice is not implemented.

Keep [decision-assisted routing](inference.md#decision-assisted-llm-routing)
disabled until the basic routes work. Enabling it sends eligible plain-text
conversation messages to a separate decision service and can add latency and cost.
Use measured descriptions and compare it with a fixed deployment before relying
on an automatic choice for customer traffic.

## Diagnose a failed first request

- **401:** use a router key beginning with `vc_`, not a session token; check expiry/revocation.
- **No eligible deployment:** inspect the task, enabled flags, policy targets,
  language, required capabilities, and any configured constraints.
- **Credential unavailable:** the referenced environment variable must exist in
  the API process or container, not only in your host shell.
- **Provider rejection:** confirm the upstream model ID, account access, options,
  voice, and output format. Inspect the request attempts in Activity.
- **Connection failure:** check container networking and the model server's bind
  address. Test a specific deployment before retrying a whole policy.

Overview aggregates all recorded requests in the rolling 24-hour window, rather
than sampling the most recent page. Its median and P95 duration include routing
and all attempts for completed requests. Cost totals remain estimates: unknown
provider and decision costs are shown separately and are never assumed to be zero.

## Run the local end-to-end check

With Go, Goose, Bun, and console dependencies installed, run:

```sh
python3 scripts/test_e2e.py
```

The harness builds the real API and production console, migrates a fresh temporary
SQLite database, and runs both services on loopback-only ports. A local fake
provider exercises console signup/configuration/session/CSRF checks, a real
playground submission with no key returned in HTML, provider fallback, streamed
chat, file transcription, speech bytes, usage/cost analytics, and activity surviving
an API restart. It then stops only its own processes and removes its temporary
database. It does not use your running installation or contact paid providers.

Go dependencies must already be cached: the harness disables module downloads.
Console dependencies must already be installed in `apps/web`. This is a local
integration check; it does not certify any external provider's model support,
quality, latency, or pricing.
