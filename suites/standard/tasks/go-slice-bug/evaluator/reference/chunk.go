package slicekit

// Chunk splits values into consecutive chunks of at most size elements.
func Chunk(values []int, size int) [][]int {
	if size <= 0 {
		panic("size must be positive")
	}
	out := make([][]int, 0, (len(values)+size-1)/size)
	for start := 0; start < len(values); start += size {
		end := start + size
		if end > len(values) {
			end = len(values)
		}
		out = append(out, values[start:end])
	}
	return out
}
