# Fix the chunking helper

`chunk.go` implements `Chunk(values []int, size int) [][]int`. It splits a slice
into consecutive chunks of at most `size` elements and must satisfy:

- every element appears exactly once, in order;
- every chunk except the last has exactly `size` elements;
- the last chunk holds the remainder, however small;
- `size <= 0` panics with `"size must be positive"`;
- an empty input returns an empty slice (not a slice holding an empty chunk).

The package's own test file fails today. Make `go test ./...` pass without
changing the test file: only `chunk.go` may be edited.
