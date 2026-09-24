package slicekit

import (
	"reflect"
	"testing"
)

func TestChunkSingleElementChunks(t *testing.T) {
	got := Chunk([]int{7, 8, 9}, 1)
	want := [][]int{{7}, {8}, {9}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Chunk = %v, want %v", got, want)
	}
}

func TestChunkSizeLargerThanInput(t *testing.T) {
	got := Chunk([]int{1, 2}, 10)
	want := [][]int{{1, 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Chunk = %v, want %v", got, want)
	}
}

func TestChunkExactMultiple(t *testing.T) {
	got := Chunk([]int{1, 2, 3, 4, 5, 6}, 3)
	want := [][]int{{1, 2, 3}, {4, 5, 6}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Chunk = %v, want %v", got, want)
	}
}
