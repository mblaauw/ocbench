# Split an amount without losing a cent

`lib/allocate.js` exports `allocate(totalCents, weights)`. It divides a whole
number of cents between payees in proportion to their weights.

Required behaviour:

- returns an array the same length as `weights`, of non-negative integers;
- the values sum to **exactly** `totalCents` — never one cent short or over;
- each value is the largest whole number of cents that keeps the result as
  close to the exact proportion as possible, and when two payees are equally
  close the earlier index receives the remaining cent (the largest-remainder
  method, ties to the left);
- throws `RangeError` when `totalCents` is negative, when `weights` is empty,
  when any weight is negative, or when every weight is zero;
- a zero weight receives zero.

The suite in `tests/` fails today. Fix `lib/allocate.js`; do not edit the tests.
