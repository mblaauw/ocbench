package pager

import "sort"

// Item is one row to page through.
type Item struct {
	ID   string
	Rank int
}

// Cursor marks a position in the ordered list.
type Cursor struct {
	Rank int
	ID   string
}

// Page returns up to size items that come after the cursor, ordered by rank
// then id, plus the cursor to continue from.
func Page(items []Item, after *Cursor, size int) ([]Item, *Cursor) {
	if size <= 0 {
		panic("size must be positive")
	}
	ordered := make([]Item, len(items))
	copy(ordered, items)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Rank != ordered[j].Rank {
			return ordered[i].Rank < ordered[j].Rank
		}
		return ordered[i].ID < ordered[j].ID
	})

	start := 0
	if after != nil {
		start = sort.Search(len(ordered), func(i int) bool {
			item := ordered[i]
			if item.Rank != after.Rank {
				return item.Rank > after.Rank
			}
			return item.ID > after.ID
		})
	}

	end := start + size
	if end > len(ordered) {
		end = len(ordered)
	}
	page := ordered[start:end]
	if len(page) == 0 {
		return nil, nil
	}
	last := page[len(page)-1]
	return page, &Cursor{Rank: last.Rank, ID: last.ID}
}
