//go:build windows

package skills

import "github.com/abigotado/confluence-cli/internal/errx"

func requireSkillMutationSupported() error {
	return &errx.Error{
		Code:    errx.CodeUsage,
		Reason:  "SKILL_MUTATION_UNSUPPORTED",
		Message: "skill installation and uninstallation are unsupported on Windows",
		Hint:    "use --dry-run to inspect the destination and install from macOS or Linux",
	}
}
