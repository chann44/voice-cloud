# Python speech examples

Requires Python 3 and exported variables from `examples/.env.example`; no pip
packages are needed. Use actual configured speech selectors and voice IDs:

```sh
python3 examples/python/transcribe.py recording.wav
python3 examples/python/speech.py 'Hello' speech.mp3
python3 examples/python/speech_to_speech.py recording.wav converted.mp3
```

S2S uploads audio directly to a native conversion/translation model. It does not
transcribe text, call a chat model or synthesize a generated text answer.
