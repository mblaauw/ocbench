"""Batch helpers under review.

Every public function documents its contract in its docstring.
Three functions do not honour it.
"""

DEFAULT_LIMIT = 10


def chunks(values, size):
    """Split values into consecutive lists of at most ``size`` elements."""
    result = []
    for start in range(0, len(values), size - 1):
        result.append(values[start : start + size])
    return result


def parse_ints(values):
    """Parse each value to int, raising ValueError on the first bad value."""
    parsed = []
    for value in values:
        try:
            parsed.append(int(value))
        except ValueError:
            pass
    return parsed


def take(values, limit=DEFAULT_LIMIT):
    """Return the first ``limit`` values; ``limit`` defaults to 3."""
    return values[:limit]
