# First working route
vc_4c0853488662b1327a7f731c56240c8774ab162513b9e9e39b9ca8d36f4db271

This walkthrough sets up a self-hosted installation and verifies one real speech
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
Use the dashboard key-entry field after configuring [encrypted storage](secrets.md),
or use an environment reference. Never put provider keys in application client code.

## Configure a speech route

1. Under **Providers**, choose an implemented speech adapter, enter its actual
   API root, save its provider API key, and enable it. See [key storage](secrets.md).
2. Under **Models**, choose the provider, discover its speech catalog or enter the
   actual audio model ID manually, and select `stt`, `tts`, or `s2s`.
3. Declare verified capabilities. TTS needs actual `voices` and `response_formats`.
   S2S needs output formats, optional voices, and declared `target_languages` for
   translation requests. Models are not downloaded or hosted by the router.
4. Use the registered `deployment:<id>` directly, or add deployments to a speech
   routing policy. Create a router key under **API keys**.
5. Open **Test a speech route**. Enter the router key and route, then type text
   for TTS or upload audio for STT/S2S. The playground accepts up to 256 KiB of
   audio; the API supports 32 MiB including multipart overhead.
6. Inspect Activity for attempts, failures, usage and estimated/unknown costs.

See [examples](../examples/README.md), [adapter coverage](adapters.md) and the
[speech API](inference.md). Native S2S directly converts/translates audio using a
speech model; there is no text-chat endpoint or STT → LLM → TTS pipeline.

Start with one verified deployment. Add fallback only after each endpoint works
individually with matching tasks, voices, formats and language capabilities.
Provider keys and actual model/voice IDs must be configured before live testing.

## Recovery and verification

[Operations](operations.md) covers encrypted-key recovery, history retention and
backups. Keep the master key separately from database backups. Run `make check`
for automated checks and `make e2e-check` for the isolated console/API/SQLite/mock
speech-provider integration. These do not certify live provider compatibility,
model quality or savings.
