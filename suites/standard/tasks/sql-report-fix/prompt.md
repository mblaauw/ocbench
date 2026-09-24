# Fix the customer order report

`report.sql` produces one row per customer: the customer's name and how many
orders they have placed, ordered by name. It must include customers who have
placed **no** orders, showing a count of 0.

`check.sh` rebuilds the database from `schema.sql` and `seed.sql`, runs
`report.sql` and compares the output with `expected.txt`. It fails today.

Fix `report.sql` only. `schema.sql`, `seed.sql`, `expected.txt` and `check.sh`
describe the contract and must not change.
