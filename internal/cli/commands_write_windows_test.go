//go:build windows

package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abigotado/confluence-cli/internal/errx"
	"github.com/abigotado/confluence-cli/internal/writepolicy"
)

func TestAllowSpacesSetFailsClosedWithoutCrossingCredentialOrNetworkBoundaries(t *testing.T) {
	app, registry, store, reader, stdout, stderr := testApp(t)
	registry.profiles["work"] = modernWriteProfile("work")
	policyPath := filepath.Join(t.TempDir(), "write-policies.json")
	app.policies = writepolicy.NewRegistry(policyPath)

	code := runApp(app, "auth", "allow-spaces", "set", "--profile", "work", "--space-id", "123", "--yes", "-o", "json")
	if code != errx.CodeInternal {
		t.Fatalf("code=%d want=%d stdout=%s stderr=%s", code, errx.CodeInternal, stdout.String(), stderr.String())
	}
	errorData := decode(t, stdout)["error"].(map[string]any)
	if errorData["code"] != "INTERNAL" || errorData["message"] != "write policy registry cannot be used safely" {
		t.Fatalf("error=%v", errorData)
	}
	if strings.Contains(stdout.String(), policyPath) || strings.Contains(stderr.String(), policyPath) {
		t.Fatalf("output exposed policy path: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if _, statErr := os.Stat(policyPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("policy file exists after fail-closed set: %v", statErr)
	}
	if store.existsCalls != 0 || store.loadCalls != 0 || reader.spaceCalls != 0 || reader.spacesCalls != 0 || reader.pageCalls != 0 || reader.createCalls != 0 || reader.updateCalls != 0 {
		t.Fatalf("fail-closed set crossed a credential or network boundary: store=%+v reader=%+v", store, reader)
	}
}
