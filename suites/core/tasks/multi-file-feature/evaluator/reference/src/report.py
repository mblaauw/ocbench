"""Rendering helpers for summary results."""

import json


def render(result):
    """Render a summary dict as a single-line JSON string."""
    return json.dumps(result)
