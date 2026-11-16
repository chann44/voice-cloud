"""Generate MP3 audio through a TTS route."""

import argparse
from client import speech


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("text")
    parser.add_argument("output", help="New MP3 file; existing files are never overwritten")
    args = parser.parse_args()
    speech(args.text, args.output)
    print(f"Saved {args.output}")


if __name__ == "__main__":
    main()
