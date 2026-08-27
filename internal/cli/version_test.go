package cli

import (
	"runtime/debug"
	"testing"
)

func TestBuildVersionPrecedenceAndVCSStamp(t *testing.T) {
	tests := []struct {
		name       string
		fallback   string
		module     string
		modified   string
		want       string
		wantCommit string
	}{
		{name: "explicit release wins over module", fallback: "v1.2.3", module: "v9.9.9", want: "v1.2.3", wantCommit: "abc123"},
		{name: "module version used for ordinary build", fallback: devVersion, module: "v2.0.0", want: "v2.0.0", wantCommit: "abc123"},
		{name: "devel normalized", fallback: "", module: "(devel)", want: devVersion, wantCommit: "abc123"},
		{name: "modified checkout is marked", fallback: "v1.2.3", module: "v9.9.9", modified: "true", want: "v1.2.3+dirty", wantCommit: "abc123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			read := func() (*debug.BuildInfo, bool) {
				return &debug.BuildInfo{
					GoVersion: "go-test",
					Main:      debug.Module{Version: tt.module},
					Settings: []debug.BuildSetting{
						{Key: "vcs.revision", Value: "abc123"},
						{Key: "vcs.time", Value: "2026-08-26T00:00:00Z"},
						{Key: "vcs.modified", Value: tt.modified},
					},
				}, true
			}
			got := buildVersion(read, tt.fallback)
			if got.Version != tt.want || got.Commit != tt.wantCommit || got.Go != "go-test" {
				t.Fatalf("buildVersion() = %+v, want version=%q commit=%q", got, tt.want, tt.wantCommit)
			}
		})
	}
}

func TestBuildVersionWithoutBuildInfo(t *testing.T) {
	got := buildVersion(func() (*debug.BuildInfo, bool) { return nil, false }, "v1.0.0")
	if got.Version != "v1.0.0" || got.Commit != "" || got.CommitTime != "" {
		t.Fatalf("buildVersion() = %+v", got)
	}
}
