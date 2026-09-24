export async function processAll(items, handler) {
  const results = [];
  for (const item of items) {
    handler(item).then((value) => results.push(value));
  }
  return results;
}
