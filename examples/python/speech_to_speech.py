"""Send audio directly to a native voice-conversion or speech-translation model."""
import argparse
from client import speech_to_speech
if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('audio')
    parser.add_argument('output')
    args = parser.parse_args()
    speech_to_speech(args.audio, args.output)
