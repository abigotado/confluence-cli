//go:build windows

package skills

import "github.com/abigotado/confluence-cli/internal/errx"

// Windows lacks the descriptor-relative detach-and-unlink primitive used by
// Unix builds. Fail closed instead of deleting a pathname after a hash TOCTOU.
func removeIfHashMatches(_, _ string, _ string) error {
	return &errx.Error{
		Code:    errx.CodeUsage,
		Reason:  "SKILL_MUTATION_UNSUPPORTED",
		Message: "skill mutation is unsupported on Windows",
		Hint:    "use --dry-run to inspect the destination and install from macOS or Linux",
	}
}
