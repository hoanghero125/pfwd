// Package version reports the build identity of pfwd: the release version,
// commit and date injected with -ldflags at release time, with a fallback to
// the build information the Go toolchain embeds in every binary.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// Link-time values, set by GoReleaser and `make build`, for example
//
//	-X github.com/hoanghero125/pfwd/internal/version.Version=0.1.0
//
// They must stay plain string variables for -X to work.
var (
	Version = ""
	Commit  = ""
	Date    = ""
)

const unknown = "unknown"

// Info describes one pfwd build.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"go"`
	Platform  string `json:"platform"`
}

// Get returns the build identity of the running binary.
func Get() Info {
	bi, _ := debug.ReadBuildInfo()
	return resolve(Version, Commit, Date, bi)
}

// String formats the build identity on one line, for `pfwd version` and bug reports.
func (i Info) String() string {
	commit := i.Commit
	if len(commit) > 12 {
		commit = commit[:12]
	}
	return fmt.Sprintf("pfwd %s (commit %s, built %s, %s %s)", i.Version, commit, i.Date, i.GoVersion, i.Platform)
}

// resolve prefers the link-time values and falls back to build info: the
// module version for `go install ...@vX.Y.Z` and the VCS stamps for a
// `go build` inside a git checkout. `go run` and `go test` get neither.
func resolve(version, commit, date string, bi *debug.BuildInfo) Info {
	if bi != nil {
		if version == "" && bi.Main.Version != "(devel)" {
			version = bi.Main.Version
		}
		for _, s := range bi.Settings {
			switch {
			case s.Key == "vcs.revision" && commit == "":
				commit = s.Value
			case s.Key == "vcs.time" && date == "":
				date = s.Value
			}
		}
	}
	if version == "" {
		version = "dev"
	}
	if commit == "" {
		commit = unknown
	}
	if date == "" {
		date = unknown
	}
	return Info{
		Version:   strings.TrimPrefix(version, "v"),
		Commit:    commit,
		Date:      date,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
}
