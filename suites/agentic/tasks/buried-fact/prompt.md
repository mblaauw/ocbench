# Find the retry limit the scheduler actually uses

The `services/` tree contains several components. Exactly one of them is the
retry scheduler's configuration, and the effective retry limit is defined there.

Report the effective retry limit the scheduler uses, and name the file that
defines it. Documentation and examples in the tree mention other numbers; the
scheduler does not read them.
