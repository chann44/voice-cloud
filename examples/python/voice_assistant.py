"""Turn one recording into a spoken answer: STT -> LLM -> TTS."""

import argparse
from pathlib import Path
from client import chat, speech, transcribe


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("audio")
    parser.add_argument("output", help="New MP3 file; existing files are never overwritten")
    args = parser.parse_args()
    if Path(args.output).exists():
        raise FileExistsError(f"Output already exists: {args.output}")
    transcript = transcribe(args.audio)
    print(f"You: {transcript}")
    reply = chat(transcript)
    print(f"Assistant: {reply}")
    speech(reply, args.output)
    print(f"Saved {args.output}")


if __name__ == "__main__":
    main()
