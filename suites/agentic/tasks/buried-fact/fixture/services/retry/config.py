"""Configuration for the retry scheduler."""

# The scheduler reads this value at import time.
MAX_RETRIES = 7


def retries():
    """Return the configured retry limit."""
    return MAX_RETRIES
