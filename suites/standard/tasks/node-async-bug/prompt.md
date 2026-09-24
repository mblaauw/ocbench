# Fix the async fan-out

`lib/queue.js` exports `processAll(items, handler)`. It must:

- call `handler` once per item, in order;
- resolve with the results **in the same order as the input**, even when the
  handler resolves at different speeds;
- reject with the handler's error if any call rejects, without waiting for the
  remaining items.

The suite in `tests/` fails today because the calls are not awaited. Fix
`lib/queue.js`; do not edit anything under `tests/`.
