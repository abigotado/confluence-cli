//go:build windows

package skills

import (
	"context"
	"errors"
	"testing"

	"github.com/abigotado/confluence-cli/internal/errx"
)

func TestWindowsSkillMutationFailsClosed(t *testing.T) {
	options := Options{Provider: ProviderClaude, Scope: ScopeUser, Dest: t.TempDir()}
	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{name: "install", run: func() error { _, err := Install(context.Background(), options); return err }},
		{name: "uninstall", run: func() error { _, err := Uninstall(context.Background(), options); return err }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			err := operation.run()
			var typed *errx.Error
			if !errors.As(err, &typed) || typed.Reason != "SKILL_MUTATION_UNSUPPORTED" || errx.ExitCode(err) != errx.CodeUsage {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
