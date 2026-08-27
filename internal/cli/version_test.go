package cli

import (
	"runtime/debug"
	"testing"

	"github.com/abigotado/confluence-cli/internal/errx"
)

func TestBuildVersionPrecedenceAndVCSStamp(t *testing.T) {
	tests := []struct {
		name               string
		fallback           string
		injectedCommit     string
		injectedCommitTime string
		module             string
		vcsCommit          string
		vcsCommitTime      string
		modified           string
		wantVersion        string
		wantCommit         string
		wantCommitTime     string
		wantErr            bool
	}{
		{
			name: "explicit release wins over module", fallback: "v1.2.3", module: "v9.9.9",
			vcsCommit: "vcs-commit", vcsCommitTime: "2026-08-26T00:00:00Z",
			wantVersion: "v1.2.3", wantCommit: "vcs-commit", wantCommitTime: "2026-08-26T00:00:00Z",
		},
		{
			name: "module version used for ordinary build", fallback: devVersion, module: "v2.0.0",
			vcsCommit: "vcs-commit", vcsCommitTime: "2026-08-26T00:00:00Z",
			wantVersion: "v2.0.0", wantCommit: "vcs-commit", wantCommitTime: "2026-08-26T00:00:00Z",
		},
		{
			name: "devel normalized", fallback: "", module: "(devel)",
			vcsCommit: "vcs-commit", vcsCommitTime: "2026-08-26T00:00:00Z",
			wantVersion: devVersion, wantCommit: "vcs-commit", wantCommitTime: "2026-08-26T00:00:00Z",
		},
		{
			name: "modified checkout is marked", fallback: "v1.2.3", module: "v9.9.9", modified: "true",
			vcsCommit: "vcs-commit", vcsCommitTime: "2026-08-26T00:00:00Z",
			wantVersion: "v1.2.3+dirty", wantCommit: "vcs-commit", wantCommitTime: "2026-08-26T00:00:00Z",
		},
		{
			name: "injected provenance overrides vcs", fallback: "v1.2.3", module: "v9.9.9", modified: "true",
			injectedCommit: "release-commit", injectedCommitTime: "2026-08-27T00:00:00Z",
			vcsCommit: "vcs-commit", vcsCommitTime: "2026-08-26T00:00:00Z",
			wantVersion: "v1.2.3+dirty", wantCommit: "release-commit", wantCommitTime: "2026-08-27T00:00:00Z",
		},
		{
			name: "injected provenance works without vcs", fallback: "v1.2.3", module: "(devel)",
			injectedCommit: "release-commit", injectedCommitTime: "2026-08-27T00:00:00Z",
			wantVersion: "v1.2.3", wantCommit: "release-commit", wantCommitTime: "2026-08-27T00:00:00Z",
		},
		{name: "vcs commit alone is omitted", fallback: "v1.2.3", vcsCommit: "vcs-commit", wantVersion: "v1.2.3"},
		{name: "vcs time alone is omitted", fallback: "v1.2.3", vcsCommitTime: "2026-08-26T00:00:00Z", wantVersion: "v1.2.3"},
		{name: "partial injected commit rejected", fallback: "v1.2.3", injectedCommit: "release-commit", wantErr: true},
		{name: "partial injected time rejected", fallback: "v1.2.3", injectedCommitTime: "2026-08-27T00:00:00Z", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			read := func() (*debug.BuildInfo, bool) {
				return &debug.BuildInfo{
					GoVersion: "go-test",
					Main:      debug.Module{Version: tt.module},
					Settings: []debug.BuildSetting{
						{Key: "vcs.revision", Value: tt.vcsCommit},
						{Key: "vcs.time", Value: tt.vcsCommitTime},
						{Key: "vcs.modified", Value: tt.modified},
					},
				}, true
			}
			got, err := buildVersion(read, tt.fallback, tt.injectedCommit, tt.injectedCommitTime)
			if tt.wantErr {
				if errx.ExitCode(err) != errx.CodeInternal {
					t.Fatalf("buildVersion() error=%v code=%d, want internal", err, errx.ExitCode(err))
				}
				return
			}
			if err != nil {
				t.Fatalf("buildVersion() error=%v", err)
			}
			if got.Version != tt.wantVersion || got.Commit != tt.wantCommit || got.CommitTime != tt.wantCommitTime || got.Go != "go-test" {
				t.Fatalf("buildVersion() = %+v, want version=%q commit=%q commitTime=%q", got, tt.wantVersion, tt.wantCommit, tt.wantCommitTime)
			}
		})
	}
}

func TestBuildVersionWithoutBuildInfo(t *testing.T) {
	got, err := buildVersion(func() (*debug.BuildInfo, bool) { return nil, false }, "v1.0.0", "release-commit", "2026-08-27T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "v1.0.0" || got.Commit != "release-commit" || got.CommitTime != "2026-08-27T00:00:00Z" {
		t.Fatalf("buildVersion() = %+v", got)
	}

	got, err = buildVersion(func() (*debug.BuildInfo, bool) { return nil, false }, "v1.0.0", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "v1.0.0" || got.Commit != "" || got.CommitTime != "" {
		t.Fatalf("buildVersion() = %+v", got)
	}
}

func TestVersionCommandRejectsPartialInjectedProvenance(t *testing.T) {
	previousVersion, previousCommit, previousCommitTime := releaseVersion, releaseCommit, releaseCommitTime
	t.Cleanup(func() {
		releaseVersion, releaseCommit, releaseCommitTime = previousVersion, previousCommit, previousCommitTime
	})
	releaseVersion = "v1.2.3"
	releaseCommit = "release-commit"
	releaseCommitTime = ""

	app, _, _, _, stdout, stderr := testApp(t)
	code := runApp(app, "version", "-o", "json")
	if code != errx.CodeInternal {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if decode(t, stdout)["error"].(map[string]any)["code"] != "INTERNAL" {
		t.Fatalf("stdout=%s", stdout.String())
	}
}
