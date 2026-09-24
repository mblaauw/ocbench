"""Summary statistics for a sequence of numbers."""


def summarize(values):
    """Return count, sum, mean, min and max for values.

    Raises ValueError when values is empty.
    """
    if not values:
        raise ValueError("empty sequence")
    total = sum(values)
    return {
        "count": len(values),
        "sum": total,
        "mean": total / len(values),
        "min": min(values),
        "max": max(values),
    }
