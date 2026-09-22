# Fix the divide operator

`calc.py` implements a small arithmetic helper. Its `divide` function returns
the wrong result for most inputs because the operator in its body is wrong.

The stdlib `unittest` suite in `tests/` describes the expected behaviour. Fix
`calc.py` so that

```
python3 -m unittest discover -s tests
```

passes. Do not change the tests.
