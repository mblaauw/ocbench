"""Command line entry point for the summarize feature."""

import sys

from src.report import render
from src.summary import summarize


def main(argv):
    """Parse numbers, summarise them and print the rendered result."""
    raise NotImplementedError("main is not implemented")


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
