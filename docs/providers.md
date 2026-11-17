# Speech provider setup

Run `make db-seed` (or `python3 scripts/seed.py --db /path/to/migrated.db`) to add
missing provider templates. New templates are disabled; repeated seeding preserves
existing URLs, credentials and enabled flags. It does not delete old provider
records or synchronize remote catalogs.

The seed is a configuration catalog, not a claim that every vendor's speech
protocol is implemented. **Providers → Implemented adapters** and authenticated
`GET /v1/adapters` are the runtime source of truth. Read [coverage and limits](adapters.md).
Text-only adapters are excluded; old records and credentials remain preserved.

1. Create/edit a provider, choose an implemented speech adapter, set its actual
   API root, save its API key, and enable it. [Encrypted storage](secrets.md) needs
   a master key configured once on the API server. Environment references remain
   an advanced alternative.
2. Open **Models** and select the provider. Load its speech catalog when available,
   or enter an actual audio model ID manually. Unclassified generic catalogs are
   not guessed to be speech models.
3. Register its task (`stt`, `tts`, `s2s`) and verified capabilities. TTS requires
   actual voices/formats. Native translation models declare `target_languages`.
4. Use the returned `deployment:<id>` selector directly or add it to a matching
   speech policy for fallback. Test a real request in the speech playground.

Local Hugging Face models need a running HTTP serving process. STT/TTS use the
compatible audio endpoints. Native S2S wrappers use the documented
[`/audio/speech-to-speech` upload contract](adapters.md#native-speech-to-speech).
Model weights and repository IDs alone do not expose inference APIs. In Docker
Desktop, use `host.docker.internal` to reach a model server on the host;
`localhost` refers to the backend container itself.

Model listings and generic reachability probes do not prove account access,
quality, model compatibility or pricing. Record only verified capabilities and
prices. Voice conversion preserves content/language; it is not speech translation.
