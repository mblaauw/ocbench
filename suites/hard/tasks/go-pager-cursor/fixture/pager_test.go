package pager

import (
	"reflect"
	"testing"
)

func TestPageWalksDistinctRanks(t *testing.T) {
	items := []Item{{"a", 1}, {"b", 2}, {"c", 3}, {"d", 4}}
	var seen []string
	var cursor *Cursor
	for {
		page, next := Page(items, cursor, 2)
		if len(page) == 0 {
			break
		}
		for _, item := range page {
			seen = append(seen, item.ID)
		}
		cursor = next
		if cursor == nil {
			break
		}
	}
	if !reflect.DeepEqual(seen, []string{"a", "b", "c", "d"}) {
		t.Fatalf("walked %v", seen)
	}
}

func TestPageRejectsNonPositiveSize(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Page did not panic for size 0")
		}
	}()
	Page(nil, nil, 0)
}
