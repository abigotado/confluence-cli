package cli

import (
	"runtime"
	"runtime/debug"

	"github.com/abigotado/confluence-cli/internal/errx"
	"github.com/spf13/cobra"
)

const devVersion = "devel"

// releaseVersion is injected for source distributions such as Homebrew.
var releaseVersion = devVersion

// releaseCommit and releaseCommitTime are injected together when a source
// distribution has no Go VCS build settings. A partial pair is rejected so a
// release never reports incomplete provenance.
var releaseCommit string
var releaseCommitTime string

func (a *App) newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use: "version", Short: "Print the version and Go build stamp", Args: usageArgs(cobra.NoArgs),
		RunE: func(_ *cobra.Command, _ []string) error {
			view, err := buildVersion(debug.ReadBuildInfo, releaseVersion, releaseCommit, releaseCommitTime)
			if err != nil {
				return err
			}
			return a.out.Success(view)
		},
	}
}

func buildVersion(read func() (*debug.BuildInfo, bool), fallback, injectedCommit, injectedCommitTime string) (versionView, error) {
	if fallback == "" {
		fallback = devVersion
	}
	releaseVersionProvided := fallback != devVersion
	view := versionView{Version: fallback, Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH}
	if (injectedCommit == "") != (injectedCommitTime == "") {
		return versionView{}, errx.Internal("release commit and commit time must be injected together")
	}
	if injectedCommit != "" {
		view.Commit = injectedCommit
		view.CommitTime = injectedCommitTime
	}
	info, ok := read()
	if !ok {
		return view, nil
	}
	if info.GoVersion != "" {
		view.Go = info.GoVersion
	}
	if !releaseVersionProvided && info.Main.Version != "" && info.Main.Version != "(devel)" {
		view.Version = info.Main.Version
	}
	var vcsCommit, vcsCommitTime string
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			vcsCommit = setting.Value
		case "vcs.time":
			vcsCommitTime = setting.Value
		case "vcs.modified":
			if setting.Value == "true" {
				view.Version += "+dirty"
			}
		}
	}
	if injectedCommit == "" && vcsCommit != "" && vcsCommitTime != "" {
		view.Commit = vcsCommit
		view.CommitTime = vcsCommitTime
	}
	return view, nil
}
