package pager

import (
	"reflect"
	"testing"
)

// walk visits every item once, following the cursor to exhaustion.
func walk(t *testing.T, items []Item, size int) []string {
	t.Helper()
	var seen []string
	var cursor *Cursor
	for step := 0; step < 20; step++ {
		page, next := Page(items, cursor, size)
		if len(page) == 0 {
			return seen
		}
		if len(page) > size {
			t.Fatalf("page of %d exceeds size %d", len(page), size)
		}
		for _, item := range page {
			seen = append(seen, item.ID)
		}
		if next == nil {
			return seen
		}
		if cursor != nil && *next == *cursor {
			t.Fatalf("cursor did not advance: %+v", *next)
		}
		cursor = next
	}
	t.Fatal("walk did not terminate")
	return nil
}

func TestPageVisitsTiedRanksExactlyOnce(t *testing.T) {
	items := []Item{
		{"a", 1}, {"b", 1}, {"c", 1}, {"d", 2}, {"e", 2}, {"f", 3},
	}
	want := []string{"a", "b", "c", "d", "e", "f"}
	for _, size := range []int{1, 2, 3, 4, 10} {
		if got := walk(t, items, size); !reflect.DeepEqual(got, want) {
			t.Errorf("size %d walked %v, want %v", size, got, want)
		}
	}
}

func TestPageOrdersByRankThenID(t *testing.T) {
	items := []Item{{"z", 1}, {"a", 1}, {"m", 0}}
	page, _ := Page(items, nil, 3)
	want := []string{"m", "a", "z"}
	var got []string
	for _, item := range page {
		got = append(got, item.ID)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("first page %v, want %v", got, want)
	}
}

func TestPageExactBoundary(t *testing.T) {
	items := []Item{{"a", 1}, {"b", 2}}
	page, next := Page(items, nil, 2)
	if len(page) != 2 || next == nil || next.ID != "b" {
		t.Fatalf("page=%v next=%v", page, next)
	}
	if page, _ := Page(items, next, 2); len(page) != 0 {
		t.Fatalf("page after the end returned %v", page)
	}
}
