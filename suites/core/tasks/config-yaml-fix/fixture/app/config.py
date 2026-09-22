"""Minimal loader for the flat YAML config used by this fixture.

Only the small subset this app needs is supported: top-level ``key: value``
lines with integer, float, boolean or bare-string scalars, plus ``#`` comments
and blank lines. Written with the standard library only.
"""

REQUIRED = ("name", "retries", "timeout_seconds")


class ConfigError(ValueError):
    """Raised when the config file is malformed or incomplete."""


def _scalar(text):
    if text in ("true", "false"):
        return text == "true"
    for cast in (int, float):
        try:
            return cast(text)
        except ValueError:
            pass
    return text.strip("\"'")


def load_config(path):
    """Load config from path and return it as a dict.

    Raises ConfigError if a required key is missing.
    """
    config = {}
    with open(path, encoding="utf-8") as handle:
        for lineno, raw in enumerate(handle, 1):
            line = raw.strip()
            if not line or line.startswith("#"):
                continue
            key, sep, value = line.partition(":")
            if not sep:
                raise ConfigError(f"{path}:{lineno}: expected 'key: value'")
            config[key.strip()] = _scalar(value.strip())

    missing = [key for key in REQUIRED if key not in config]
    if missing:
        raise ConfigError(f"{path}: missing required key(s): {', '.join(missing)}")
    return config
