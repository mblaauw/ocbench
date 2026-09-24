#!/bin/sh
# Rebuild the database, run the report and compare with expected.txt.
set -eu
cd "$(dirname "$0")"
db="$(mktemp)"
trap 'rm -f "$db"' EXIT
sqlite3 "$db" < schema.sql
sqlite3 "$db" < seed.sql
actual="$(sqlite3 -separator '|' "$db" < report.sql)"
expected="$(cat expected.txt)"
if [ "$actual" != "$expected" ]; then
  echo "report mismatch"
  echo "--- expected"; printf '%s\n' "$expected"
  echo "--- actual";   printf '%s\n' "$actual"
  exit 1
fi
echo "report ok"
