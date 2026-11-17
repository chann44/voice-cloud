# TypeScript speech examples

Requires Bun and exported variables from `examples/.env.example`. Use real model
selectors and voice IDs configured in the dashboard. From the repository root:

```sh
bun run examples/typescript/transcribe.ts recording.wav
bun run examples/typescript/speech.ts 'Hello' speech.mp3
bun run examples/typescript/speech-to-speech.ts recording.wav converted.mp3
```

Native S2S sends audio directly to the selected model. Set `TARGET_LANGUAGE` only
for a translation model declaring that target; omit `VOICE` when that model does
not need a voice. For voice conversion, set an actual supported `VOICE` ID.
