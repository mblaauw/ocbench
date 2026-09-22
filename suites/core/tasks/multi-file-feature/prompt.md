# Implement the summarize feature

The `src/` package, `cli.py` and `tests/` sketch a small "summarize" feature,
but every entry point currently raises `NotImplementedError`:

- `src/summary.py` — `summarize(values)` must return a dict with the keys
  `count`, `sum`, `mean`, `min` and `max`. Empty input raises `ValueError`.
- `src/report.py` — `render(result)` must return the summary as a single-line
  JSON string.
- `cli.py` — `main(argv)` must parse `argv` as numbers, summarise them, print
  the rendered result and return `0`.

Make the unit tests in `tests/` pass:

```
python3 -m unittest discover -s tests
```

and the end-to-end stdlib smoke check pass:

```
python3 smoke.py
```

`smoke.py` and the tests describe the expected behaviour; do not change
`smoke.py`.
