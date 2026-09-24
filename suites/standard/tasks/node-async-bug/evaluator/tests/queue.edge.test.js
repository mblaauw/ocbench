import assert from "node:assert/strict";
import { test } from "node:test";

import { processAll } from "../lib/queue.js";

test("rejects when a handler rejects", async () => {
  await assert.rejects(
    () =>
      processAll([1, 2, 3], async (item) => {
        if (item === 2) throw new Error("boom");
        return item;
      }),
    /boom/,
  );
});

test("single item keeps its result", async () => {
  assert.deepEqual(await processAll(["only"], async (item) => item + "!"), ["only!"]);
});

test("results follow input order for many items", async () => {
  const items = Array.from({ length: 12 }, (_, i) => i);
  const results = await processAll(items, async (item) => {
    await new Promise((resolve) => setTimeout(resolve, (12 - item) % 5));
    return item * 2;
  });
  assert.deepEqual(results, items.map((i) => i * 2));
});
