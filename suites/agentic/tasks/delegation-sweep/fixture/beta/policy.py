"""Retention policy for the beta service."""

# How long this service keeps its records before expiring them.
RETENTION_DAYS = 90

def window():
    """Return the retention window in days."""
    return RETENTION_DAYS
