# curl

Follow [shared setup](../README.md) to export the router URL, key, and selectors.
These commands run on your backend machine. They need curl 7.83+ (for
`--no-clobber`); they install no packages and create no application server.

## Chat

```sh
curl --fail-with-body --silent --show-error \
  "$VOICE_CLOUD_URL/v1/chat/completions" \
  -H "Authorization: Bearer $VOICE_CLOUD_API_KEY" \
  -H 'Content-Type: application/json' \
  -d "{\"model\":\"$LLM_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Explain how rain forms in one sentence.\"}]}"
```

## Stream chat

```sh
curl --fail-with-body --silent --show-error --no-buffer \
  "$VOICE_CLOUD_URL/v1/chat/completions" \
  -H "Authorization: Bearer $VOICE_CLOUD_API_KEY" \
  -H 'Content-Type: application/json' \
  -d "{\"model\":\"$LLM_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Tell me a short story.\"}],\"stream\":true}"
```

The response is raw SSE: `data:` events, followed by `data: [DONE]` on a successful
chat stream. Use the provider's compatible streaming support. The router will not
restart a response after output has begun. This is text streaming, not a realtime
audio conversation.

## Transcribe a recording

```sh
curl --fail-with-body --silent --show-error \
  "$VOICE_CLOUD_URL/v1/audio/transcriptions" \
  -H "Authorization: Bearer $VOICE_CLOUD_API_KEY" \
  --form-string "model=$STT_MODEL" \
  --form-string "language=$AUDIO_LANGUAGE" \
  --form-string 'response_format=json' \
  -F 'file=@recording.wav'
```

Do not set a multipart Content-Type header manually; curl supplies its boundary.
Replace `recording.wav` with a provider-supported recording under the upload limit.

## Generate MP3 speech

```sh
curl --fail --silent --show-error --no-clobber \
  "$VOICE_CLOUD_URL/v1/audio/speech" \
  -H "Authorization: Bearer $VOICE_CLOUD_API_KEY" \
  -H 'Content-Type: application/json' \
  -d "{\"model\":\"$TTS_MODEL\",\"input\":\"Your appointment is confirmed.\",\"voice\":\"$VOICE\",\"response_format\":\"mp3\"}" \
  --output speech.mp3
```

`--no-clobber` keeps an existing `speech.mp3` and selects a numbered filename.
Check curl's exit status before using the saved output: an interrupted transfer
may leave an incomplete file. The voice and MP3 format must work on all selected
deployments. Use the Python or TypeScript examples to chain a transcript, chat
reply, and speech response in an application.
