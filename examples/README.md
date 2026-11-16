# Application examples

Run these examples on your backend or development machine. Your web/mobile client
calls your backend; your backend calls Voice Cloud. Keep router keys and provider
credentials on the server.

| Directory | Runtime | Examples |
| --- | --- | --- |
| [typescript](typescript/) | Bun, no packages to install | Chat, transcription, MP3 speech, recorded voice assistant |
| [python](python/) | Python 3.10+, standard library | Chat, transcription, MP3 speech, recorded voice assistant |
| [curl](curl/) | curl and a POSIX shell | Chat, streamed chat, transcription, MP3 speech |

## Configure once

1. Start Voice Cloud and open the console at `http://localhost:3000`.
2. Add an enabled OpenAI-compatible provider connection. Set its `credential_ref`
   to the name of a provider-key environment variable available to the API process.
   Local endpoints that do not require authentication can leave it empty.
3. Register models and deployments with their actual upstream model IDs.
4. Create an LLM policy, an STT policy, and a TTS policy. Add matching deployments
   as targets; configure priority and fallback there. You only need the policies
   used by the example you run.
5. Create a router API key in the console. A management session token will not work.

From the repository root:

```sh
cp examples/.env.example examples/.env
# Edit examples/.env: set your router key, actual policy IDs, and supported voice.
set -a
. ./examples/.env
set +a
```

`examples/.env` is ignored by Git. Scripts read the environment; they do not load
this file themselves. The API origin has no `/v1` suffix. If your app runs in a
container, `localhost` refers to that container; use the backend's reachable host.

The model selectors are router resources, such as `policy:2`, not provider model
names. Changing policy targets lets the app keep the same selector. To pin a
specific deployment, set the relevant variable to `deployment:<id>`. Policy IDs
are examples, not seeded resources. The key's default policy is unnecessary
because these examples select a policy explicitly for each task.

## Use in your application

Import the functions from either language's `client` module into your own backend
handler. Return chat text, a transcript, or audio through that handler to your
frontend. The clients send the same three HTTP endpoints documented in the
[inference guide](../docs/inference.md); they are small examples, not a published SDK.

The recorded voice-assistant example performs one turn:

```text
Recording → STT policy → transcript → LLM policy → reply → TTS policy → MP3
```

It does not record the microphone, play audio, keep conversation history, or open
a realtime voice session. TTS examples request MP3: all selected deployments must
support that format and the configured voice. Matching voice names across different
providers may require separate policies. Native provider protocols without an
implemented adapter cannot serve these requests.

Bodies are limited to 32 MiB including multipart overhead. Client calls wait up to
330 seconds; the router allows five minutes after parsing the upload. The examples
make no application-level retries; fallback is configured on the router. HTTP
errors include the router request ID when available, which you can inspect in
Activity. The CLI examples print transcripts/replies to your terminal.

Speech scripts refuse existing output filenames. Choose a new path for each run.
See the language READMEs for commands and the focused offline checks to run later.
