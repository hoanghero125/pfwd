package version

import (
	"runtime/debug"
	"testing"
)

func TestResolve(t *testing.T) {
	tests := []struct {
		name                          string
		version, commit, date         string
		bi                            *debug.BuildInfo
		wantVer, wantCommit, wantDate string
	}{
		{
			name:    "ldflags win over build info",
			version: "0.1.0", commit: "0123456789abcdef", date: "2026-10-11T09:12:00Z",
			bi:      buildInfo("v9.9.9", "ffffffff", "2000-01-01T00:00:00Z"),
			wantVer: "0.1.0", wantCommit: "0123456789abcdef", wantDate: "2026-10-11T09:12:00Z",
		},
		{
			name:    "go install at a tag strips the v",
			bi:      buildInfo("v0.1.0", "", ""),
			wantVer: "0.1.0", wantCommit: "unknown", wantDate: "unknown",
		},
		{
			name:    "go build in a git checkout uses VCS stamps",
			bi:      buildInfo("v0.0.0-20261011111015-4f534bcc0d0c+dirty", "4f534bcc0d0c1111", "2026-10-11T11:10:15Z"),
			wantVer: "0.0.0-20261011111015-4f534bcc0d0c+dirty", wantCommit: "4f534bcc0d0c1111", wantDate: "2026-10-11T11:10:15Z",
		},
		{
			name:    "go run reports a dev build",
			bi:      buildInfo("(devel)", "", ""),
			wantVer: "dev", wantCommit: "unknown", wantDate: "unknown",
		},
		{
			name:    "no build info at all",
			wantVer: "dev", wantCommit: "unknown", wantDate: "unknown",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolve(tt.version, tt.commit, tt.date, tt.bi)
			if got.Version != tt.wantVer || got.Commit != tt.wantCommit || got.Date != tt.wantDate {
				t.Errorf("resolve() = %q/%q/%q, want %q/%q/%q",
					got.Version, got.Commit, got.Date, tt.wantVer, tt.wantCommit, tt.wantDate)
			}
			if got.GoVersion == "" || got.Platform == "" {
				t.Errorf("resolve() left GoVersion %q or Platform %q empty", got.GoVersion, got.Platform)
			}
		})
	}
}

func TestInfoString(t *testing.T) {
	i := Info{
		Version: "0.1.0", Commit: "4f534bcc0d0c99999999", Date: "2026-10-11T11:10:15Z",
		GoVersion: "go1.27.2", Platform: "linux/amd64",
	}
	want := "pfwd 0.1.0 (commit 4f534bcc0d0c, built 2026-10-11T11:10:15Z, go1.27.2 linux/amd64)"
	if got := i.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func buildInfo(mainVersion, revision, vcsTime string) *debug.BuildInfo {
	bi := &debug.BuildInfo{Main: debug.Module{Path: "github.com/hoanghero125/pfwd", Version: mainVersion}}
	if revision != "" {
		bi.Settings = append(bi.Settings, debug.BuildSetting{Key: "vcs.revision", Value: revision})
	}
	if vcsTime != "" {
		bi.Settings = append(bi.Settings, debug.BuildSetting{Key: "vcs.time", Value: vcsTime})
	}
	return bi
}
