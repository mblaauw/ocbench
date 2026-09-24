import assert from "node:assert/strict";
import { test } from "node:test";

import { allocate } from "../lib/allocate.js";

test("splits evenly", () => {
  assert.deepEqual(allocate(100, [1, 1, 1, 1]), [25, 25, 25, 25]);
});

test("splits proportionally when it divides exactly", () => {
  assert.deepEqual(allocate(300, [1, 2]), [100, 200]);
});

test("never loses a cent when it does not divide evenly", () => {
  const parts = allocate(100, [1, 1, 1]);
  assert.equal(
    parts.reduce((a, b) => a + b, 0),
    100,
    `parts ${parts} do not sum to 100`,
  );
});

test("weights a single payee with everything", () => {
  assert.deepEqual(allocate(7, [3]), [7]);
});
