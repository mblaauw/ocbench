package slicekit

// Chunk splits values into consecutive chunks of at most size elements.
func Chunk(values []int, size int) [][]int {
	if size <= 0 {
		panic("size must be positive")
	}
	var out [][]int
	for start := 0; start < len(values); start += size {
		end := start + size
		if end >= len(values) {
			end = len(values) - 1
		}
		out = append(out, values[start:end])
	}
	return out
}
