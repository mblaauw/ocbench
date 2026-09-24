export function allocate(totalCents, weights) {
  const sum = weights.reduce((a, b) => a + b, 0);
  return weights.map((weight) => Math.round((totalCents * weight) / sum));
}
