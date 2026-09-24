"""Retention policy for the gamma service."""

# How long this service keeps its records before expiring them.
RETENTION_DAYS = 7

def window():
    """Return the retention window in days."""
    return RETENTION_DAYS
