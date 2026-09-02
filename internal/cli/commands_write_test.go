package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abigotado/confluence-cli/internal/auth"
	"github.com/abigotado/confluence-cli/internal/errx"
	"github.com/abigotado/confluence-cli/internal/profile"
	"github.com/abigotado/confluence-cli/internal/writepolicy"
)

const pageBodySentinel = "<p>PAGE_BODY_SENTINEL</p>"

func writeBodyFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "page.storage")
	if err := os.WriteFile(path, []byte(pageBodySentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func modernWriteProfile(name string) profile.Profile {
	value := testProfile(name)
	value.CredentialGeneration = strings.Repeat("A", 43)
	value.Capabilities = []profile.Capability{profile.CapabilityRead, profile.CapabilityPageWrite}
	return value
}

func confirmedPageIntent(t *testing.T, action, spaceID, pageID, parentID string, expectedVersion int, title string) string {
	t.Helper()
	identity := writepolicy.IdentityFor(modernWriteProfile("work"))
	receipt, err := newPageMutationReceipt(action, "work", identity, spaceID, pageID, parentID, expectedVersion, title, []byte(pageBodySentinel), false)
	if err != nil {
		t.Fatal(err)
	}
	return receipt.IntentSHA256
}

func TestPageCreateDryRunIsPurelyLocalAndOmitsBody(t *testing.T) {
	app, registry, store, reader, stdout, stderr := testApp(t)
	registry.profiles["work"] = modernWriteProfile("work")
	app.policies = nil
	bodyFile := writeBodyFile(t)
	code := runApp(app,
		"pages", "create", "--profile", "work", "--space-id", "123", "--title", "Local preview",
		"--body-file", bodyFile, "--representation", "storage", "--dry-run", "-o", "json",
	)
	if code != errx.CodeOK {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if store.existsCalls != 0 || store.loadCalls != 0 || reader.spaceCalls != 0 || reader.createCalls != 0 {
		t.Fatalf("dry-run crossed a boundary: store=%+v reader=%+v", store, reader)
	}
	if strings.Contains(stdout.String(), pageBodySentinel) || strings.Contains(stderr.String(), pageBodySentinel) {
		t.Fatal("dry-run exposed page body")
	}
	data := decode(t, stdout)["data"].(map[string]any)
	if data["dry_run"] != true || data["applied"] != false || data["remote_checks"] != "not_performed" || data["content_sha256"] == "" || data["intent_sha256"] == "" {
		t.Fatalf("receipt=%v", data)
	}
}

func TestPageWriteProjectionAndConfirmationGateBeforeBoundaries(t *testing.T) {
	tests := []struct {
		name string
		args []string
		code errx.Code
	}{
		{name: "confirmation", args: []string{"pages", "create", "--profile", "work", "--space-id", "123", "--title", "Created", "--body-file", "BODY"}, code: errx.CodeConfirm},
		{name: "projection", args: []string{"pages", "create", "--profile", "work", "--space-id", "123", "--title", "Created", "--body-file", "BODY", "--yes", "--fields", "body"}, code: errx.CodeUsage},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app, _, store, reader, stdout, _ := testApp(t)
			bodyFile := writeBodyFile(t)
			for index := range test.args {
				if test.args[index] == "BODY" {
					test.args[index] = bodyFile
				}
			}
			test.args = append(test.args, "-o", "json")
			if code := runApp(app, test.args...); code != test.code {
				t.Fatalf("code=%d want=%d output=%s", code, test.code, stdout.String())
			}
			if store.loadCalls != 0 || reader.spaceCalls != 0 || reader.createCalls != 0 {
				t.Fatalf("gate crossed boundary: store=%+v reader=%+v", store, reader)
			}
		})
	}
}

func TestCredentialBindingMismatchBlocksReadNetwork(t *testing.T) {
	app, registry, store, reader, stdout, _ := testApp(t)
	selected := modernWriteProfile("work")
	registry.profiles["work"] = selected
	store.credentials["work"] = auth.Credential{
		Token: "replacement-token", ProfileIdentity: profile.CredentialIdentity(selected), Generation: strings.Repeat("A", 42) + "Q",
		Capabilities: append([]profile.Capability(nil), selected.Capabilities...),
	}
	code := runApp(app, "spaces", "get", "123", "--profile", "work", "-o", "json")
	if code != errx.CodeAuth || reader.spaceCalls != 0 {
		t.Fatalf("code=%d space=%d output=%s", code, reader.spaceCalls, stdout.String())
	}
	if decode(t, stdout)["error"].(map[string]any)["code"] != "CREDENTIAL_BINDING_MISMATCH" {
		t.Fatalf("output=%s", stdout.String())
	}
}

func TestExpiredProfileBlocksGuardedWritesBeforeCredentialOrNetwork(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "create",
			args: []string{"pages", "create", "--profile", "work", "--space-id", "123", "--title", "Created", "--body-file", "BODY", "--confirm-intent", confirmedPageIntent(t, "pages.create", "123", "", "", 0, "Created"), "--yes", "-o", "json"},
		},
		{
			name: "update",
			args: []string{"pages", "update", "456", "--profile", "work", "--space-id", "123", "--expected-version", "7", "--title", "Updated", "--body-file", "BODY", "--confirm-intent", confirmedPageIntent(t, "pages.update", "123", "456", "", 7, "Updated"), "--yes", "-o", "json"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app, registry, store, reader, stdout, _ := testApp(t)
			registry.profiles["work"] = modernWriteProfile("work")
			app.policies = writepolicy.NewRegistry(filepath.Join(t.TempDir(), "write-policies.json"))
			expired := time.Now().Add(-24 * time.Hour)
			selected := registry.profiles["work"]
			selected.ExpiresAt = &expired
			registry.profiles["work"] = selected
			bodyFile := writeBodyFile(t)
			for index := range test.args {
				if test.args[index] == "BODY" {
					test.args[index] = bodyFile
				}
			}
			code := runApp(app, test.args...)
			if code != errx.CodeAuth || store.loadCalls != 0 || reader.spaceCalls != 0 || reader.pageCalls != 0 || reader.createCalls != 0 || reader.updateCalls != 0 {
				t.Fatalf("code=%d load=%d space=%d page=%d create=%d update=%d output=%s", code, store.loadCalls, reader.spaceCalls, reader.pageCalls, reader.createCalls, reader.updateCalls, stdout.String())
			}
			if decode(t, stdout)["error"].(map[string]any)["code"] != "TOKEN_EXPIRED" {
				t.Fatalf("output=%s", stdout.String())
			}
		})
	}
}

func TestAllowSpacesRejectsLegacyProfile(t *testing.T) {
	app, registry, _, _, stdout, _ := testApp(t)
	registry.profiles["work"] = testProfile("work")
	app.policies = writepolicy.NewRegistry(filepath.Join(t.TempDir(), "write-policies.json"))
	code := runApp(app, "auth", "allow-spaces", "set", "--profile", "work", "--space-id", "123", "--yes", "-o", "json")
	if code != errx.CodePermission {
		t.Fatalf("code=%d output=%s", code, stdout.String())
	}
	if decode(t, stdout)["error"].(map[string]any)["code"] != "PAGE_WRITE_CAPABILITY_REQUIRED" {
		t.Fatalf("output=%s", stdout.String())
	}
}
