package version

import (
	"fmt"
	"runtime/debug"
)

var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// BuildInfo is named to avoid colliding with the Info() accessor.
type BuildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

func current() BuildInfo {
	i := BuildInfo{Version: Version, Commit: Commit, Date: Date}
	if i.Commit == "unknown" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			for _, s := range bi.Settings {
				if s.Key == "vcs.revision" {
					i.Commit = s.Value
				}
			}
		}
	}
	if i.Commit == "" {
		i.Commit = "unknown"
	}
	return i
}

func Info() BuildInfo { return current() }

func String() string {
	i := current()
	return fmt.Sprintf("ocbench %s (%s)", i.Version, i.Commit)
}
