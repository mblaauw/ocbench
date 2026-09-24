import assert from "node:assert/strict";
import { test } from "node:test";

import { processAll } from "../lib/queue.js";

test("preserves input order when handlers resolve out of order", async () => {
  const delays = { a: 30, b: 10, c: 0 };
  const results = await processAll(["a", "b", "c"], async (item) => {
    await new Promise((resolve) => setTimeout(resolve, delays[item]));
    return item.toUpperCase();
  });
  assert.deepEqual(results, ["A", "B", "C"]);
});

test("calls the handler once per item", async () => {
  const seen = [];
  await processAll([1, 2, 3], async (item) => {
    seen.push(item);
    return item;
  });
  assert.deepEqual(seen, [1, 2, 3]);
});

test("empty input resolves to an empty list", async () => {
  assert.deepEqual(await processAll([], async () => 1), []);
});
