package opencode

import (
	"errors"
	"regexp"
	"strings"
	"unicode"
)

// ansiRE matches CSI escape sequences such as "\x1b[90m" and "\x1b[0m".
var ansiRE = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")

// boxRunes are the box-drawing characters the `mcp list` table uses for its
// frame. They are stripped from the left of a line before extracting content.
const boxRunes = "\u2502\u250c\u2510\u2514\u2518\u251c\u2524\u252c\u2534\u253c\u2500\u2501\u250f\u2513\u2517\u251b\u2523\u252b\u2533\u253b\u254b\u256d\u256e\u256f\u2570"

// stripANSI removes ANSI escape sequences from s.
func stripANSI(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}

// parseVersion returns the first non-empty, trimmed line of the `--version`
// output.
func parseVersion(b []byte) (string, error) {
	for _, line := range strings.Split(string(b), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			return s, nil
		}
	}
	return "", errors.New("opencode: empty version output")
}

// parseMCPList parses the human-readable `opencode mcp list` table. A server
// line contains the filled-circle marker U+25CF and is optionally followed by a
// second status marker; the target is the next line that carries content.
// Zero servers yield an empty (nil) slice and no error.
func parseMCPList(b []byte) ([]MCPStatus, error) {
	lines := strings.Split(stripANSI(string(b)), "\n")
	var out []MCPStatus
	for i, line := range lines {
		if !strings.ContainsRune(line, '\u25cf') {
			continue
		}
		rest := line[strings.IndexRune(line, '\u25cf')+len("\u25cf"):]
		rest = strings.TrimLeft(rest, "\u25cf\u25cb \t")
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		_, second := serverMarkers(line)
		enabled := second == '\u25cf' || !strings.Contains(strings.ToLower(rest), "disabled")
		out = append(out, MCPStatus{
			Name:    fields[0],
			Enabled: enabled,
			Target:  nextTarget(lines, i),
		})
	}
	return out, nil
}

// serverMarkers returns the first and second status markers on an mcp server
// line (for example ● and ○ in "●  ○ gitlab disabled").
func serverMarkers(line string) (first, second rune) {
	seen := 0
	for _, r := range strings.TrimSpace(line) {
		switch {
		case r == '\u25cf' || r == '\u25cb':
			seen++
			if seen == 1 {
				first = r
			} else if seen == 2 {
				second = r
			}
		case unicode.IsSpace(r) && seen > 0:
			// separator between markers or before the name
		default:
			return first, second
		}
	}
	return first, second
}

// nextTarget returns the trimmed content of the first line after index i that
// is not blank or purely box-drawing. A following server line terminates the
// search (the server has no target).
func nextTarget(lines []string, i int) string {
	for j := i + 1; j < len(lines); j++ {
		if strings.ContainsRune(lines[j], '\u25cf') {
			return ""
		}
		cand := strings.TrimSpace(strings.TrimLeft(lines[j], boxRunes+" \t"))
		if cand == "" || isBoxOnly(cand) {
			continue
		}
		return cand
	}
	return ""
}

// isBoxOnly reports whether s consists solely of whitespace and box-drawing
// characters.
func isBoxOnly(s string) bool {
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		if !strings.ContainsRune(boxRunes, r) {
			return false
		}
	}
	return true
}
