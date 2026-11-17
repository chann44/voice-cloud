# Speech routing examples

Voice Cloud supports STT, TTS and native S2S only. Configure an actual speech
provider/deployment and router API key, copy `.env.example` to `.env`, replace
selectors and voice IDs, then export those variables in your shell.

- [TypeScript](typescript/README.md): transcription, synthesis, native S2S.
- [Python](python/README.md): the same tasks using the standard library.
- [curl](curl/README.md): HTTP requests without an SDK.

`S2S_MODEL` selects a native voice-conversion or translation model. `VOICE` is the
actual provider voice ID; translation models can omit it when no voice is needed.
Set `TARGET_LANGUAGE` only for a native translation deployment declaring that
language. ElevenLabs voice conversion does not translate spoken content.

Use `policy:<id>` for compatible fallback targets, or `deployment:<id>` to isolate
one endpoint. Keep router keys on the application server and provider keys in
Voice Cloud's encrypted store. Examples refuse to overwrite output files.
No model is downloaded/hosted by these examples and no text-chat pipeline is used.
