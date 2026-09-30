package web

// The dashboard renders stored RFC3339 stamps in three widths: a two-line table
// cell, a bare date, and a snapshot stamp. They live together so the formats
// stay comparable.

// shortTime splits an RFC3339 stamp into the date and time a table cell shows
// on two lines, which keeps the column narrow.
func shortTime(stamp string) (date, clock string) {
	if len(stamp) >= 16 {
		return stamp[5:10], stamp[11:16]
	}
	return stamp, ""
}

// shortDate renders just the date part of an RFC3339 stamp.
func shortDate(stamp string) string {
	date, _ := shortTime(stamp)
	return date
}

// stampText renders an RFC3339 stamp as "YYYY-MM-DD HH:MM", the precision a
// profile snapshot shows. shortDate drops the time, which would make two
// snapshots on the same day look identical.
func stampText(stamp string) string {
	if len(stamp) >= 16 {
		return stamp[:10] + " " + stamp[11:16]
	}
	return stamp
}
