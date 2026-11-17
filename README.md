<p align="center">
  <img src="apps/site/static/logo.svg" alt="Voice Cloud logo" width="220">
</p>

<h1 align="center">Voice Cloud</h1>

**An open router for voice.** Connect local and cloud voice models through one router, with policies for language, latency, and cost.

Voice Cloud is being built in Go for self-hosted deployments, with support for voice applications and agents.

> **Early development.** The features below describe the direction of the project. APIs and setup are still taking shape.

## What we're building

- **Local and cloud models** — connect models running on your hardware or hosted by a provider.
- **Policy-based routing** — choose models based on language, latency, and cost requirements.
- **Activity, logs, and analytics** — see which models handle requests and track usage, latency, and cost.
- **Agent support** — use the router as the voice layer for agents.
- **Self-hosting** — run the router on your own infrastructure.

## Repository layout

| Path | What it is |
| --- | --- |
| `apps/api` | Go management and inference API |
| `apps/worker` | Go background worker: deployment health checks and cleanup |
| `apps/web` | The console (SvelteKit) |
| `apps/site` | The landing page (static SvelteKit) |
| `db` | SQLite migrations and queries |
| `docker` | Compose stack for the backend and console |
| `docs` | Guides for the API, worker, providers, users, and Docker |
| `examples` | Runnable TypeScript, Python, and curl integrations |

## Run it

From the repository root, with Docker Compose v2.22+:

```sh
docker compose -f docker/compose.yml up --build -d
```

The console is at `http://localhost:3000` and the API at `http://localhost:8080`.
Open the console to create the first administrator. Follow the [guided setup](docs/quickstart.md) to test a route in the console playground. See [the Docker guide](docs/docker.md)
for development mode and [the API guide](docs/api.md) for running without Docker.

Configure a speech provider, model, deployment and matching routing policy, then
create a router key. Voice Cloud supports **text-to-speech, speech-to-text and
native speech-to-speech only**. The [speech API](docs/inference.md) and
[implemented adapters](docs/adapters.md) cover routing, fallback and provider
limits. Native S2S consumes audio directly and returns audio; there is no text-chat
endpoint or STT → LLM → TTS pipeline. WebSocket conversations are not implemented.


See [application examples](examples/README.md) for TypeScript, Python and curl
transcription, synthesis and native speech-to-speech requests.

## Landing page

The marketing site lives in `apps/site` and builds to static files:

```sh
cd apps/site
bun install
bun run dev      # http://localhost:5174
bun run build    # output in apps/site/build
```

Its "Open app" and "Docs" links are set in `apps/site/src/lib/links.ts`, or with
`VITE_APP_URL` and `VITE_DOCS_URL` at build time. See [its README](apps/site/README.md).

## Reliability checks and operations

See [operations and recovery](docs/operations.md) for automatic backups, verified
restores, request recovery, and retention. The default Compose stack includes a
daily backup service; copy backups off-host for protection against host loss.

```sh
make check       # API/worker race tests, database and backup checks, examples, console
make e2e-check   # Real local API + console against mock providers; no paid calls
```

Routing quality and real vendor compatibility must also be evaluated against your
configured deployments. Local mock-provider tests prove protocol and failure
handling, not model quality or invoice accuracy.
