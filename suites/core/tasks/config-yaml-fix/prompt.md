# Fix the misspelled config key

`app/config.py` loads `config/app.yaml` and requires the keys `name`,
`retries` and `timeout_seconds`. The config file has a typo in one of those
keys, so the stdlib `unittest` suite in `tests/` fails:

```
python3 -m unittest discover -s tests
```

Correct the config so the tests pass. Change only `config/app.yaml`; do not
edit the loader or the tests.
