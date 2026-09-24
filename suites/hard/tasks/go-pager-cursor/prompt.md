# Fix cursor pagination with tied ranks

`pager.Page` walks a list of items ordered by `(Rank, ID)`:

```go
type Item struct { ID string; Rank int }
type Cursor struct { Rank int; ID string }
func Page(items []Item, after *Cursor, size int) ([]Item, *Cursor)
```

Required behaviour:

- items are ordered by `Rank`, then `ID`;
- the returned page starts strictly after `after` in that order (a nil cursor
  starts at the beginning);
- the page holds at most `size` items; `size <= 0` panics with
  `"size must be positive"`;
- the returned cursor is the `(Rank, ID)` of the last item on the page, or nil
  when the page is empty or ends the list;
- walking with the returned cursor visits every item exactly once, even when
  many items share the same `Rank` — this is where the current implementation
  loses rows.

The package test passes today because it only uses distinct ranks. Fix
`pager.go`; the test file must not change.
