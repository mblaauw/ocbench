export function allocate(totalCents, weights) {
  if (!Number.isInteger(totalCents) || totalCents < 0) {
    throw new RangeError("totalCents must be a non-negative integer");
  }
  if (weights.length === 0) {
    throw new RangeError("weights must not be empty");
  }
  if (weights.some((w) => w < 0)) {
    throw new RangeError("weights must not be negative");
  }
  const sum = weights.reduce((a, b) => a + b, 0);
  if (sum === 0) {
    throw new RangeError("weights must not all be zero");
  }

  const exact = weights.map((w) => (totalCents * w) / sum);
  const parts = exact.map(Math.floor);
  let remaining = totalCents - parts.reduce((a, b) => a + b, 0);

  const order = weights
    .map((_, index) => index)
    .sort((a, b) => {
      const diff = exact[b] - Math.floor(exact[b]) - (exact[a] - Math.floor(exact[a]));
      return diff !== 0 ? diff : a - b;
    });

  for (const index of order) {
    if (remaining <= 0) break;
    parts[index] += 1;
    remaining -= 1;
  }
  return parts;
}
