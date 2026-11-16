"""Transcribe an existing audio file through an STT route."""

import argparse
from client import transcribe


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("audio")
    args = parser.parse_args()
    print(transcribe(args.audio))


if __name__ == "__main__":
    main()
