# Python examples

Python 3.10+; standard library only. These scripts run on a server or trusted
local machine. Never put a router key in browser code or a distributed mobile app.

Start and configure Voice Cloud using the [examples setup](../README.md).
Set these environment variables in your shell (IDs below are placeholders):

```sh
export VOICE_CLOUD_URL=http://localhost:8080
export VOICE_CLOUD_API_KEY=vc_your_router_key
export LLM_MODEL=policy:1
export STT_MODEL=policy:2
export TTS_MODEL=policy:3
export VOICE=alloy
export AUDIO_LANGUAGE=en
```

Use your own policy IDs, or `deployment:<id>` to select a single deployment.
`VOICE_CLOUD_URL` is the router origin without `/v1`; it defaults to
`http://localhost:8080`. Each script requires only the model variables it uses.
`VOICE` defaults to `alloy`; choose a voice supported by **every deployment in
your TTS policy**. `AUDIO_LANGUAGE` defaults to `en`; set it to an empty string
to let the transcription provider detect the language without a routing filter.

Run from the repository root:

| Example | Command |
| --- | --- |
| Chat | `python3 examples/python/chat.py 'Explain policy routing.'` |
| Transcribe a recording | `python3 examples/python/transcribe.py recording.wav` |
| Generate MP3 speech | `python3 examples/python/speech.py 'Hello from Voice Cloud.' speech.mp3` |
| Recorded voice assistant | `python3 examples/python/voice_assistant.py recording.wav reply.mp3` |

`client.py` shows the shared HTTP calls. Transcription sends multipart audio;
chat sends JSON; speech saves the returned MP3 bytes. HTTP errors include the
router's request ID when available, which you can inspect under Activity. Output
paths must not already exist. Requests can incur provider charges.

The voice assistant performs **one recorded turn**: file → STT → LLM → TTS →
new MP3 file. It does not record a microphone, play audio, maintain conversation
history, or establish a realtime session. It prints the transcript and answer
locally; avoid retaining that output if it contains private information.

The router accepts at most 32 MiB including multipart overhead. Use an audio
format supported by all STT targets. No example downloads a model or embeds
provider credentials; provider secrets belong in the router's API process.

Offline regression checks are provided for the later testing session:

```sh
python3 -m unittest discover -s examples/python -p 'test_*.py'
```
