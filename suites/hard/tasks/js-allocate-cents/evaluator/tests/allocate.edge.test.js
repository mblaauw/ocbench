import assert from "node:assert/strict";
import { test } from "node:test";

import { allocate } from "../lib/allocate.js";

const sum = (xs) => xs.reduce((a, b) => a + b, 0);

test("uses the largest remainder, ties to the left", () => {
  assert.deepEqual(allocate(100, [1, 1, 1]), [34, 33, 33]);
  assert.deepEqual(allocate(10, [1, 1, 1, 1]), [3, 3, 2, 2]);
});

test("gives a zero weight nothing", () => {
  assert.deepEqual(allocate(10, [0, 1, 1]), [0, 5, 5]);
  assert.deepEqual(allocate(0, [1, 1]), [0, 0]);
});

test("throws on invalid input", () => {
  assert.throws(() => allocate(-1, [1]), RangeError);
  assert.throws(() => allocate(10, []), RangeError);
  assert.throws(() => allocate(10, [1, -1]), RangeError);
  assert.throws(() => allocate(10, [0, 0]), RangeError);
});

test("always sums to the total", () => {
  const cases = [
    [1, [1, 1, 1]],
    [7, [1, 2, 3]],
    [999, [5, 3, 2]],
    [1000, [1, 1, 1, 1, 1, 1, 1]],
    [3, [1, 1, 1, 1, 1]],
  ];
  for (const [total, weights] of cases) {
    const parts = allocate(total, weights);
    assert.equal(parts.length, weights.length, `length for ${weights}`);
    assert.equal(sum(parts), total, `sum for total=${total} weights=${weights}`);
    assert.ok(
      parts.every((p) => Number.isInteger(p) && p >= 0),
      `non-integer or negative part in ${parts}`,
    );
  }
});
