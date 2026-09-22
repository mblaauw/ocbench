"""Basic aggregate helpers."""


def minimum(values):
    """Return the smallest value in values."""
    if not values:
        raise ValueError("empty sequence")
    result = values[0]
    for value in values[1:]:
        if value < result:
            result = value
    return result


def maximum(values):
    """Return the largest value in values."""
    if not values:
        raise ValueError("empty sequence")
    result = values[0]
    for value in values[1:]:
        if value < result:
            result = value
    return result


def mean(values):
    """Return the arithmetic mean of values."""
    if not values:
        raise ValueError("empty sequence")
    return sum(values) / len(values)
