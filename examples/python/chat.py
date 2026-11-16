"""Send one user message through an LLM route."""

import argparse
from client import chat


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("prompt")
    args = parser.parse_args()
    print(chat(args.prompt))


if __name__ == "__main__":
    main()
