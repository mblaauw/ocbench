package web

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

// navItem is one sidebar link.
type navItem struct {
	Label   string
	Href    string
	Hint    string
	Current bool
}

// envRow is one fact in the sidebar's environment panel.
type envRow struct {
	Label string
	Value string
}

// scopeItem is one suite selector entry.
type scopeItem struct {
	Label   string
	Href    string
	Current bool
}

// layout is the chrome every page shares: the sidebar, the page header and the
// optional suite scope switcher.
type layout struct {
	Title     string
	Crumb     string
	PageTitle string
	Sub       string

	Nav   []navItem
	Env   []envRow
	Scope []scopeItem
	// ShowScope is false on pages that are not suite-scoped.
	ShowScope bool
}

// navItems lists the pages the dashboard serves. Pages that are not built yet
// are simply absent rather than shown as dead links.
func navItems(current string, hints map[string]string) []navItem {
	order := []struct{ key, label, href string }{
		{"overview", "Overview", "/"},
		{"runs", "Runs", "/runs"},
		{"profiles", "Architecture", "/arch"},
	}
	out := make([]navItem, 0, len(order))
	for _, o := range order {
		out = append(out, navItem{
			Label: o.label, Href: o.href, Hint: hints[o.key], Current: o.key == current,
		})
	}
	return out
}

// envRows reports stored facts only: the dashboard never runs doctor or any
// other subprocess, so it shows what the store already knows.
func (h *handler) envRows(ctx context.Context) []envRow {
	rows := []envRow{{Label: "Read-only", Value: "127.0.0.1"}}
	if h.store == nil {
		return rows
	}
	count, err := h.store.CountRuns(ctx)
	if err != nil {
		return rows
	}
	rows = append([]envRow{{Label: "Store", Value: strconv.Itoa(count) + " runs"}}, rows...)

	runs, err := h.store.ListRuns(ctx, 1, "")
	if err != nil || len(runs) == 0 {
		return rows
	}
	last := runs[0]
	rows = append([]envRow{{Label: "OpenCode", Value: last.OpenCodeVersion}}, rows...)
	if ts, err := time.Parse(time.RFC3339, last.StartedAt); err == nil {
		rows = append(rows, envRow{Label: "Last run", Value: ts.Format("02 Jan 15:04")})
	}
	return rows
}

// scopeItems builds the suite switcher, linking back to the given path with a
// scope parameter.
func scopeItems(path, current string, suites []string) []scopeItem {
	item := func(label, value string) scopeItem {
		q := url.Values{}
		if value != "" {
			q.Set("scope", value)
		}
		href := path
		if encoded := q.Encode(); encoded != "" {
			href += "?" + encoded
		}
		return scopeItem{Label: label, Href: href, Current: current == value}
	}
	out := []scopeItem{item("all", "")}
	for _, s := range suites {
		out = append(out, item(s, s))
	}
	return out
}
