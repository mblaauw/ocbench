#!/bin/sh
# A second dataset the task never saw: a customer with no orders and one with
# several, which catches a fix that special-cases the visible seed.
set -eu
cd "$(dirname "$0")/.."
db="$(mktemp)"
trap 'rm -f "$db"' EXIT
sqlite3 "$db" < schema.sql
sqlite3 "$db" <<'SQL'
INSERT INTO customers (id, name) VALUES (10, 'Zoe'), (11, 'Yan');
INSERT INTO orders (id, customer_id, total_cents) VALUES (10, 11, 1), (11, 11, 2), (12, 11, 3);
SQL
actual="$(sqlite3 -separator '|' "$db" < report.sql)"
expected="Yan|3
Zoe|0"
if [ "$actual" != "$expected" ]; then
  echo "edge report mismatch"
  echo "--- expected"; printf '%s\n' "$expected"
  echo "--- actual";   printf '%s\n' "$actual"
  exit 1
fi
echo "edge report ok"
