export async function processAll(items, handler) {
  const results = [];
  for (const item of items) {
    results.push(await handler(item));
  }
  return results;
}
