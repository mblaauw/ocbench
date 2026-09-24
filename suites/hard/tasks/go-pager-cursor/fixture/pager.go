package pager

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
	var page []Item
	for _, item := range items {
		if after != nil && item.Rank <= after.Rank {
			continue
		}
		page = append(page, item)
		if len(page) == size {
			break
		}
	}
	if len(page) == 0 {
		return nil, nil
	}
	last := page[len(page)-1]
	return page, &Cursor{Rank: last.Rank, ID: last.ID}
}
