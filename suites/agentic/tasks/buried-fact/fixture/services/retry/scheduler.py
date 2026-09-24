"""Retry scheduler."""

from services.retry.config import retries


def attempts():
    """Return how many attempts a job gets."""
    return retries() + 1
