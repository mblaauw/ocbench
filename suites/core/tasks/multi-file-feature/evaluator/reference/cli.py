"""Command line entry point for the summarize feature."""

import sys

from src.report import render
from src.summary import summarize


def main(argv):
    """Parse numbers, summarise them and print the rendered result."""
    values = [int(arg) for arg in argv]
    print(render(summarize(values)))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
