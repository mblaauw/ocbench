package slicekit

import (
	"reflect"
	"testing"
)

func TestChunkEvenly(t *testing.T) {
	got := Chunk([]int{1, 2, 3, 4}, 2)
	want := [][]int{{1, 2}, {3, 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Chunk = %v, want %v", got, want)
	}
}

func TestChunkRemainder(t *testing.T) {
	got := Chunk([]int{1, 2, 3, 4, 5}, 2)
	want := [][]int{{1, 2}, {3, 4}, {5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Chunk = %v, want %v", got, want)
	}
}

func TestChunkEmpty(t *testing.T) {
	if got := Chunk(nil, 3); len(got) != 0 {
		t.Fatalf("Chunk(nil) = %v, want empty", got)
	}
}

func TestChunkRejectsNonPositiveSize(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Chunk did not panic for size 0")
		}
	}()
	Chunk([]int{1}, 0)
}
