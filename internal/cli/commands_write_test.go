package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abigotado/confluence-cli/internal/auth"
	"github.com/abigotado/confluence-cli/internal/confluence"
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

func configureGuardedWrite(t *testing.T, app *App, registry *fakeRegistry, store *fakeStore, reader *fakeReader) {
	t.Helper()
	selected := modernWriteProfile("work")
	if err := selected.Validate(); err != nil {
		t.Fatalf("test profile invalid: %v", err)
	}
	registry.profiles[selected.Name] = selected
	store.credentials[selected.Name] = auth.Credential{
		Token: "stored-token", ProfileIdentity: profile.CredentialIdentity(selected), Generation: selected.CredentialGeneration,
		Capabilities: append([]profile.Capability(nil), selected.Capabilities...),
	}
	policyDir := filepath.Join(t.TempDir(), "config")
	if err := os.Mkdir(policyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	app.policies = writepolicy.NewRegistry(filepath.Join(policyDir, "write-policies.json"))
	if _, err := app.policies.Set(context.Background(), selected, []string{"123"}); err != nil {
		t.Fatal(err)
	}
	reader.space = confluence.Space{ID: "123"}
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

func TestPageCreateRejectsProfileReplacementAfterDryRun(t *testing.T) {
	app, registry, store, reader, stdout, _ := testApp(t)
	configureGuardedWrite(t, app, registry, store, reader)
	bodyFile := writeBodyFile(t)
	previewArgs := []string{
		"pages", "create", "--profile", "work", "--space-id", "123", "--title", "Created",
		"--body-file", bodyFile, "--dry-run", "-o", "json",
	}
	if code := runApp(app, previewArgs...); code != errx.CodeOK {
		t.Fatalf("dry-run code=%d output=%s", code, stdout.String())
	}
	approved := decode(t, stdout)["data"].(map[string]any)["intent_sha256"].(string)

	replaced := modernWriteProfile("work")
	replaced.CredentialGeneration = strings.Repeat("A", 42) + "Q"
	registry.profiles["work"] = replaced
	if _, err := app.policies.Set(context.Background(), replaced, []string{"123"}); err != nil {
		t.Fatal(err)
	}
	store.credentials["work"] = auth.Credential{
		Token: "replacement-token", ProfileIdentity: profile.CredentialIdentity(replaced), Generation: replaced.CredentialGeneration,
		Capabilities: append([]profile.Capability(nil), replaced.Capabilities...),
	}
	stdout.Reset()
	code := runApp(app,
		"pages", "create", "--profile", "work", "--space-id", "123", "--title", "Created", "--body-file", bodyFile,
		"--confirm-intent", approved, "--yes", "-o", "json",
	)
	if code != errx.CodeConfirm || store.loadCalls != 0 || reader.spaceCalls != 0 || reader.createCalls != 0 {
		t.Fatalf("code=%d load=%d space=%d create=%d output=%s", code, store.loadCalls, reader.spaceCalls, reader.createCalls, stdout.String())
	}
	if decode(t, stdout)["error"].(map[string]any)["code"] != "WRITE_INTENT_CONFIRMATION_REQUIRED" {
		t.Fatalf("output=%s", stdout.String())
	}
}

func TestPageCreateUsesAllowlistPreflightAndOneMutation(t *testing.T) {
	app, registry, store, reader, stdout, stderr := testApp(t)
	configureGuardedWrite(t, app, registry, store, reader)
	reader.created = confluence.Page{ID: "456", SpaceID: "123", Title: "Created", Status: "current", Version: confluence.PageVersion{Number: 1}}
	bodyFile := writeBodyFile(t)
	code := runApp(app,
		"pages", "create", "--profile", "work", "--space-id", "123", "--title", "Created",
		"--body-file", bodyFile, "--confirm-intent", confirmedPageIntent(t, "pages.create", "123", "", "", 0, "Created"), "--yes", "-o", "json",
	)
	if code != errx.CodeOK {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if store.loadCalls != 1 || reader.spaceCalls != 1 || reader.createCalls != 1 {
		t.Fatalf("boundary calls load=%d space=%d create=%d", store.loadCalls, reader.spaceCalls, reader.createCalls)
	}
	data := decode(t, stdout)["data"].(map[string]any)
	if data["page_id"] != "456" || data["applied"] != true || data["remote_checks"] != "performed" {
		t.Fatalf("receipt=%v", data)
	}
}

func TestPageUpdateRejectsStalePreflightWithoutMutation(t *testing.T) {
	app, registry, store, reader, stdout, _ := testApp(t)
	configureGuardedWrite(t, app, registry, store, reader)
	reader.page = confluence.Page{ID: "456", SpaceID: "123", Status: "current", Version: confluence.PageVersion{Number: 8}}
	bodyFile := writeBodyFile(t)
	code := runApp(app,
		"pages", "update", "456", "--profile", "work", "--space-id", "123", "--expected-version", "7",
		"--title", "Updated", "--body-file", bodyFile, "--confirm-intent", confirmedPageIntent(t, "pages.update", "123", "456", "", 7, "Updated"), "--yes", "-o", "json",
	)
	if code != errx.CodeConflict || reader.updateCalls != 0 {
		t.Fatalf("code=%d updateCalls=%d output=%s", code, reader.updateCalls, stdout.String())
	}
	if decode(t, stdout)["error"].(map[string]any)["code"] != "STALE_PAGE_VERSION" {
		t.Fatalf("output=%s", stdout.String())
	}
}

func TestPageWriteProjectionAndConfirmationGateBeforeBoundaries(t *testing.T) {
	tests := []struct {
		name string
		args []string
		code errx.Code
	}{
		{name: "confirmation", args: []string{"pages", "create", "--profile", "work", "--space-id", "123", "--title", "Created", "--body-file", "BODY"}, code: errx.CodeConfirm},
		{name: "intent confirmation", args: []string{"pages", "create", "--profile", "work", "--space-id", "123", "--title", "Created", "--body-file", "BODY", "--confirm-intent", strings.Repeat("0", 64), "--yes"}, code: errx.CodeConfirm},
		{name: "projection", args: []string{"pages", "create", "--profile", "work", "--space-id", "123", "--title", "Created", "--body-file", "BODY", "--yes", "--fields", "body"}, code: errx.CodeUsage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, registry, store, reader, stdout, _ := testApp(t)
			if tt.name == "intent confirmation" {
				configureGuardedWrite(t, app, registry, store, reader)
			}
			bodyFile := writeBodyFile(t)
			for i := range tt.args {
				if tt.args[i] == "BODY" {
					tt.args[i] = bodyFile
				}
			}
			tt.args = append(tt.args, "-o", "json")
			if code := runApp(app, tt.args...); code != tt.code {
				t.Fatalf("code=%d want=%d output=%s", code, tt.code, stdout.String())
			}
			if store.loadCalls != 0 || reader.spaceCalls != 0 || reader.createCalls != 0 {
				t.Fatalf("gate crossed boundary: store=%+v reader=%+v", store, reader)
			}
		})
	}
}

func TestPageCreateRejectsDeniedAllowlistBeforeCredentialOrNetwork(t *testing.T) {
	app, registry, store, reader, stdout, _ := testApp(t)
	configureGuardedWrite(t, app, registry, store, reader)
	bodyFile := writeBodyFile(t)
	code := runApp(app,
		"pages", "create", "--profile", "work", "--space-id", "999", "--title", "Denied",
		"--body-file", bodyFile, "--confirm-intent", confirmedPageIntent(t, "pages.create", "999", "", "", 0, "Denied"), "--yes", "-o", "json",
	)
	if code != errx.CodePermission || store.loadCalls != 0 || reader.spaceCalls != 0 || reader.createCalls != 0 {
		t.Fatalf("code=%d load=%d space=%d create=%d output=%s", code, store.loadCalls, reader.spaceCalls, reader.createCalls, stdout.String())
	}
	if decode(t, stdout)["error"].(map[string]any)["code"] != "SPACE_NOT_ALLOWED" {
		t.Fatalf("output=%s", stdout.String())
	}
}

func TestCredentialBindingMismatchBlocksReadAndWriteNetwork(t *testing.T) {
	t.Run("read", func(t *testing.T) {
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
	})

	t.Run("write", func(t *testing.T) {
		app, registry, store, reader, stdout, _ := testApp(t)
		configureGuardedWrite(t, app, registry, store, reader)
		store.credentials["work"] = auth.Credential{
			Token: "replacement-token", ProfileIdentity: profile.CredentialIdentity(modernWriteProfile("work")), Generation: strings.Repeat("A", 42) + "Q",
			Capabilities: []profile.Capability{profile.CapabilityRead, profile.CapabilityPageWrite},
		}
		bodyFile := writeBodyFile(t)
		code := runApp(app,
			"pages", "create", "--profile", "work", "--space-id", "123", "--title", "Created", "--body-file", bodyFile,
			"--confirm-intent", confirmedPageIntent(t, "pages.create", "123", "", "", 0, "Created"), "--yes", "-o", "json",
		)
		if code != errx.CodeAuth || reader.spaceCalls != 0 || reader.createCalls != 0 {
			t.Fatalf("code=%d space=%d create=%d output=%s", code, reader.spaceCalls, reader.createCalls, stdout.String())
		}
		if decode(t, stdout)["error"].(map[string]any)["code"] != "CREDENTIAL_BINDING_MISMATCH" {
			t.Fatalf("output=%s", stdout.String())
		}
	})
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
			configureGuardedWrite(t, app, registry, store, reader)
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

func TestPageCreateRejectsParentOutsideSpaceBeforeMutation(t *testing.T) {
	app, registry, store, reader, stdout, _ := testApp(t)
	configureGuardedWrite(t, app, registry, store, reader)
	reader.page = confluence.Page{ID: "456", SpaceID: "999", Status: "current", Version: confluence.PageVersion{Number: 1}}
	bodyFile := writeBodyFile(t)
	code := runApp(app,
		"pages", "create", "--profile", "work", "--space-id", "123", "--parent-id", "456", "--title", "Child",
		"--body-file", bodyFile, "--confirm-intent", confirmedPageIntent(t, "pages.create", "123", "", "456", 0, "Child"), "--yes", "-o", "json",
	)
	if code != errx.CodeConflict || store.loadCalls != 1 || reader.pageCalls != 1 || reader.createCalls != 0 {
		t.Fatalf("code=%d load=%d page=%d create=%d output=%s", code, store.loadCalls, reader.pageCalls, reader.createCalls, stdout.String())
	}
	if decode(t, stdout)["error"].(map[string]any)["code"] != "TARGET_CHANGED" {
		t.Fatalf("output=%s", stdout.String())
	}
}

func TestPageUpdatePreservesPreflightParent(t *testing.T) {
	app, registry, store, reader, stdout, _ := testApp(t)
	configureGuardedWrite(t, app, registry, store, reader)
	reader.page = confluence.Page{ID: "456", SpaceID: "123", ParentID: "777", Status: "current", Version: confluence.PageVersion{Number: 7}}
	reader.updated = confluence.Page{ID: "456", SpaceID: "123", ParentID: "777", Title: "Updated", Status: "current", Version: confluence.PageVersion{Number: 8}}
	bodyFile := writeBodyFile(t)
	code := runApp(app,
		"pages", "update", "456", "--profile", "work", "--space-id", "123", "--expected-version", "7", "--title", "Updated",
		"--body-file", bodyFile, "--confirm-intent", confirmedPageIntent(t, "pages.update", "123", "456", "", 7, "Updated"), "--yes", "-o", "json",
	)
	if code != errx.CodeOK || reader.updateCalls != 1 || reader.lastUpdate.ParentID != "777" {
		t.Fatalf("code=%d update=%d parent=%q output=%s", code, reader.updateCalls, reader.lastUpdate.ParentID, stdout.String())
	}
	if decode(t, stdout)["data"].(map[string]any)["parent_id"] != "777" {
		t.Fatalf("output=%s", stdout.String())
	}
}

func TestPageCreateReportsAppliedWhenProfileLockFinalizationFails(t *testing.T) {
	app, registry, store, reader, stdout, _ := testApp(t)
	configureGuardedWrite(t, app, registry, store, reader)
	reader.created = confluence.Page{ID: "456", SpaceID: "123", Title: "Created", Status: "current", Version: confluence.PageVersion{Number: 1}}
	registry.lockFinalErr = errors.New("profile lock finalization failed")
	bodyFile := writeBodyFile(t)
	code := runApp(app,
		"pages", "create", "--profile", "work", "--space-id", "123", "--title", "Created",
		"--body-file", bodyFile, "--confirm-intent", confirmedPageIntent(t, "pages.create", "123", "", "", 0, "Created"), "--yes", "-o", "json",
	)
	if code != errx.CodeInternal || reader.createCalls != 1 {
		t.Fatalf("code=%d create=%d output=%s", code, reader.createCalls, stdout.String())
	}
	if decode(t, stdout)["error"].(map[string]any)["code"] != "WRITE_APPLIED_LOCAL_FAILURE" {
		t.Fatalf("output=%s", stdout.String())
	}
}

func TestPageCreatePreservesUnknownOutcome(t *testing.T) {
	app, registry, store, reader, stdout, _ := testApp(t)
	configureGuardedWrite(t, app, registry, store, reader)
	reader.createErr = errx.WriteOutcomeUnknown("create page")
	bodyFile := writeBodyFile(t)
	code := runApp(app,
		"pages", "create", "--profile", "work", "--space-id", "123", "--title", "Created",
		"--body-file", bodyFile, "--confirm-intent", confirmedPageIntent(t, "pages.create", "123", "", "", 0, "Created"), "--yes", "-o", "json",
	)
	if code != errx.CodeConflict || reader.createCalls != 1 {
		t.Fatalf("code=%d calls=%d output=%s", code, reader.createCalls, stdout.String())
	}
	if decode(t, stdout)["error"].(map[string]any)["code"] != "WRITE_OUTCOME_UNKNOWN" {
		t.Fatalf("output=%s", stdout.String())
	}
}

func TestAllowSpacesSetShowAndClear(t *testing.T) {
	app, registry, store, _, stdout, stderr := testApp(t)
	selected := modernWriteProfile("work")
	registry.profiles["work"] = selected
	store.credentials["work"] = auth.Credential{Token: "stored-token"}
	policyDir := filepath.Join(t.TempDir(), "config")
	if err := os.Mkdir(policyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	app.policies = writepolicy.NewRegistry(filepath.Join(policyDir, "write-policies.json"))

	if code := runApp(app, "auth", "allow-spaces", "set", "--profile", "work", "--space-id", "456", "--space-id", "123", "--yes", "-o", "json"); code != errx.CodeOK {
		t.Fatalf("set code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	data := decode(t, stdout)["data"].(map[string]any)
	spaces := data["spaces"].([]any)
	if len(spaces) != 2 || spaces[0] != "123" || spaces[1] != "456" || data["applied"] != true {
		t.Fatalf("set receipt=%v", data)
	}

	stdout.Reset()
	if code := runApp(app, "auth", "allow-spaces", "show", "--profile", "work", "-o", "json"); code != errx.CodeOK {
		t.Fatalf("show code=%d output=%s", code, stdout.String())
	}
	if decode(t, stdout)["data"].(map[string]any)["state"] != "bound" {
		t.Fatalf("show output=%s", stdout.String())
	}

	stdout.Reset()
	if code := runApp(app, "auth", "allow-spaces", "clear", "--profile", "work", "--yes", "-o", "json"); code != errx.CodeOK {
		t.Fatalf("clear code=%d output=%s", code, stdout.String())
	}
	if _, err := app.policies.Get(context.Background(), "work"); !errors.Is(err, writepolicy.ErrNotFound) {
		t.Fatalf("policy after clear error=%v", err)
	}
}

func TestAllowSpacesRejectsLegacyProfile(t *testing.T) {
	app, registry, _, _, stdout, _ := testApp(t)
	registry.profiles["work"] = testProfile("work")
	policyDir := filepath.Join(t.TempDir(), "config")
	if err := os.Mkdir(policyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	app.policies = writepolicy.NewRegistry(filepath.Join(policyDir, "write-policies.json"))
	code := runApp(app, "auth", "allow-spaces", "set", "--profile", "work", "--space-id", "123", "--yes", "-o", "json")
	if code != errx.CodePermission {
		t.Fatalf("code=%d output=%s", code, stdout.String())
	}
	if decode(t, stdout)["error"].(map[string]any)["code"] != "PAGE_WRITE_CAPABILITY_REQUIRED" {
		t.Fatalf("output=%s", stdout.String())
	}
}
