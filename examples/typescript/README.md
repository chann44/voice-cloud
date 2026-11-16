# TypeScript / Bun

Follow [shared setup](../README.md), then run from the repository root. Bun runs
these TypeScript files directly; no dependency installation is required.

```sh
bun run examples/typescript/chat.ts "Explain how rain forms in one sentence."
bun run examples/typescript/transcribe.ts recording.wav
bun run examples/typescript/speech.ts "Your appointment is confirmed." speech.mp3
bun run examples/typescript/voice-assistant.ts recording.wav reply.mp3
```

Chat needs `LLM_MODEL`, transcription needs `STT_MODEL`, speech needs `TTS_MODEL`
and `VOICE`. The assistant uses all three policies. `VOICE` defaults to `alloy`;
`AUDIO_LANGUAGE` defaults to `en`. Both must suit your deployments.

Use [client.ts](client.ts) from your backend, for example:

```ts
import { chat, transcribe, speech } from './client';

const reply = await chat('Hello!');
const transcript = await transcribe('/path/to/upload.wav');
const audio = await speech(reply); // MP3 bytes as an ArrayBuffer
```

Send `audio` back with `Content-Type: audio/mpeg` when exposing it from your app.
These functions accept server-side file paths; upload handling belongs in your
application. Do not import this module into a browser bundle.

Offline checks for a later testing session (no provider calls):

```sh
bun test examples/typescript/client.test.ts
```
