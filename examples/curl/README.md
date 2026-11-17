# Speech HTTP examples

Export `VOICE_CLOUD_API_KEY`, `STT_MODEL`, `TTS_MODEL`, `S2S_MODEL`, and an actual
supported `VOICE`. Replace selectors with your configured policies/deployments.

```sh
curl http://localhost:8080/v1/audio/transcriptions \
  -H "Authorization: Bearer $VOICE_CLOUD_API_KEY" \
  -F "model=$STT_MODEL" -F file=@recording.wav

curl http://localhost:8080/v1/audio/speech \
  -H "Authorization: Bearer $VOICE_CLOUD_API_KEY" \
  -H 'Content-Type: application/json' \
  -d "{\"model\":\"$TTS_MODEL\",\"input\":\"Hello\",\"voice\":\"$VOICE\"}" \
  --output speech.mp3

curl http://localhost:8080/v1/audio/speech-to-speech \
  -H "Authorization: Bearer $VOICE_CLOUD_API_KEY" \
  -F "model=$S2S_MODEL" -F file=@recording.wav -F "voice=$VOICE" \
  -F response_format=mp3 --output converted.mp3
```

For native translation, omit `voice` if the model does not require it and add
`-F target_language=fr` only when the deployment declares that language. Shell
redirection/output flags can overwrite files; choose new output filenames.
