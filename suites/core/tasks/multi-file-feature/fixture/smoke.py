"""End-to-end smoke check for the summarize CLI (stdlib only)."""

import json
from contextlib import redirect_stdout
from io import StringIO

import cli


def main():
    buf = StringIO()
    with redirect_stdout(buf):
        code = cli.main(["1", "2", "3", "4"])
    assert code == 0, code
    result = json.loads(buf.getvalue())
    expected = {"count": 4, "sum": 10, "mean": 2.5, "min": 1, "max": 4}
    assert result == expected, result
    print("smoke ok")


if __name__ == "__main__":
    main()
