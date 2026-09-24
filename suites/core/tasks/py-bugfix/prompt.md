# Fix the divide operator

`calc.py` implements a small arithmetic helper. Its `divide(a, b)` function
returns the wrong result for most inputs because the operator in its body is
wrong.

Expected behaviour:

- `divide(a, b)` returns `a / b` — an exact float when the division is even
  (`divide(10, 2) == 5.0`) and the usual float otherwise
  (`divide(7, 2) ≈ 3.5`).
- `divide(1, 0)` raises `ZeroDivisionError`.
- `add(a, b)` is already correct and must not change.

The fix belongs in `calc.py`; only that file may change. The behaviour above is
checked after you finish, so make the implementation correct rather than
tailoring it to any particular caller.
