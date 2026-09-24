"""Retention policy for the alpha service."""

# How long this service keeps its records before expiring them.
RETENTION_DAYS = 30

def window():
    """Return the retention window in days."""
    return RETENTION_DAYS
