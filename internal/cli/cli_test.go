package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abigotado/confluence-cli/internal/auth"
	"github.com/abigotado/confluence-cli/internal/confluence"
	"github.com/abigotado/confluence-cli/internal/errx"
	"github.com/abigotado/confluence-cli/internal/profile"
)

const cliTokenSentinel = "CLI_TOKEN_SENTINEL_MUST_NOT_APPEAR"

type fakeRegistry struct {
	mu           sync.Mutex
	profiles     map[string]profile.Profile
	lockFinalErr error
}

func (registry *fakeRegistry) WithProfileLock(_ context.Context, _ string, fn func() error) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err := fn(); err != nil {
		return err
	}
	return registry.lockFinalErr
}

func (registry *fakeRegistry) List(context.Context) ([]profile.Profile, error) {
	result := make([]profile.Profile, 0, len(registry.profiles))
	for _, value := range registry.profiles {
		result = append(result, value)
	}
	return result, nil
}

func (registry *fakeRegistry) Get(_ context.Context, name string) (profile.Profile, error) {
	value, ok := registry.profiles[name]
	if !ok {
		return profile.Profile{}, profile.ErrNotFound
	}
	return value, nil
}

func (registry *fakeRegistry) Add(_ context.Context, value profile.Profile) error {
	if _, exists := registry.profiles[value.Name]; exists {
		return profile.ErrAlreadyExists
	}
	registry.profiles[value.Name] = value
	return nil
}

func (registry *fakeRegistry) Put(_ context.Context, value profile.Profile) error {
	registry.profiles[value.Name] = value
	return nil
}

func (registry *fakeRegistry) Remove(_ context.Context, name string) error {
	if _, exists := registry.profiles[name]; !exists {
		return profile.ErrNotFound
	}
	delete(registry.profiles, name)
	return nil
}

type fakeStore struct {
	credentials  map[string]auth.Credential
	existsCalls  int
	loadCalls    int
	migrateCalls int
}

func (store *fakeStore) Exists(_ context.Context, name string) (bool, error) {
	store.existsCalls++
	_, ok := store.credentials[name]
	return ok, nil
}

func (store *fakeStore) Load(_ context.Context, name string) (auth.Credential, error) {
	store.loadCalls++
	value, ok := store.credentials[name]
	if !ok {
		return auth.Credential{}, auth.ErrNotFound
	}
	return value, nil
}

func (store *fakeStore) Save(_ context.Context, name string, value auth.Credential) error {
	store.credentials[name] = value
	return nil
}

func (store *fakeStore) Delete(_ context.Context, name string) error {
	delete(store.credentials, name)
	return nil
}

func (store *fakeStore) MigrateKeychain(context.Context, string) error {
	store.migrateCalls++
	return nil
}

type fakeReader struct {
	verifyErr   error
	verifyCalls int
	verifyHook  func()
	spaces      confluence.PageResult[confluence.Spaces]
	space       confluence.Space
	page        confluence.Page
	created     confluence.Page
	updated     confluence.Page
	createErr   error
	updateErr   error
	spaceCalls  int
	spacesCalls int
	pageCalls   int
	createCalls int
	updateCalls int
	lastUpdate  confluence.UpdatePageInput
}

func (reader *fakeReader) ListSpaces(context.Context, confluence.ListOptions) (confluence.PageResult[confluence.Spaces], error) {
	reader.spacesCalls++
	return reader.spaces, nil
}
func (reader *fakeReader) GetSpace(context.Context, string) (confluence.Space, error) {
	reader.spaceCalls++
	return reader.space, nil
}
func (*fakeReader) ListPages(context.Context, confluence.PageListOptions) (confluence.PageResult[confluence.Pages], error) {
	return confluence.PageResult[confluence.Pages]{}, nil
}
func (reader *fakeReader) GetPage(context.Context, string, string) (confluence.Page, error) {
	reader.pageCalls++
	return reader.page, nil
}
func (*fakeReader) Search(context.Context, string, confluence.ListOptions) (confluence.PageResult[confluence.SearchResults], error) {
	return confluence.PageResult[confluence.SearchResults]{}, nil
}
func (reader *fakeReader) VerifyRequiredAccess(context.Context) error {
	reader.verifyCalls++
	if reader.verifyHook != nil {
		reader.verifyHook()
	}
	return reader.verifyErr
}

func (reader *fakeReader) CreatePage(context.Context, confluence.CreatePageInput) (confluence.Page, error) {
	reader.createCalls++
	return reader.created, reader.createErr
}

func (reader *fakeReader) UpdatePage(_ context.Context, input confluence.UpdatePageInput) (confluence.Page, error) {
	reader.updateCalls++
	reader.lastUpdate = input
	return reader.updated, reader.updateErr
}

type spyReader struct {
	value string
	read  bool
}

func (reader *spyReader) Read(buffer []byte) (int, error) {
	reader.read = true
	if reader.value == "" {
		return 0, io.EOF
	}
	count := copy(buffer, reader.value)
	reader.value = reader.value[count:]
	return count, nil
}

func testProfile(name string) profile.Profile {
	return profile.Profile{Name: name, Site: "https://tenant.atlassian.net", Email: "user@example.com", CloudID: "cloud-id"}
}

func testApp(t *testing.T) (*App, *fakeRegistry, *fakeStore, *fakeReader, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	registry := &fakeRegistry{profiles: map[string]profile.Profile{}}
	store := &fakeStore{credentials: map[string]auth.Credential{}}
	reader := &fakeReader{}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{
		registry: registry, store: store, stdin: strings.NewReader(""), stdout: stdout, stderr: stderr, now: time.Now,
		discoverCloudID: func(context.Context, string) (string, error) { return "cloud-id", nil },
	}
	app.newClient = func(p profile.Profile, credential auth.Credential, _ *slog.Logger) (confluenceReader, error) {
		if credential.Token != cliTokenSentinel && credential.Token != "stored-token" {
			t.Error("newClient received an unexpected token")
		}
		return reader, nil
	}
	return app, registry, store, reader, stdout, stderr
}

func runApp(app *App, args ...string) errx.Code {
	return app.Run(context.Background(), app.NewRootCommand(), args)
}

func decode(t *testing.T, output *bytes.Buffer) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(output.Bytes(), &value); err != nil {
		t.Fatalf("decode output: %v\n%s", err, output.String())
	}
	return value
}

func TestAuthListNeverReadsKeychain(t *testing.T) {
	app, registry, store, _, stdout, stderr := testApp(t)
	registry.profiles["work"] = testProfile("work")
	if code := runApp(app, "auth", "list", "-o", "json"); code != errx.CodeOK {
		t.Fatalf("code=%d output=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if store.existsCalls != 0 || store.loadCalls != 0 {
		t.Fatalf("auth list touched Keychain boundary: exists=%d load=%d", store.existsCalls, store.loadCalls)
	}
}

func TestAuthLoginRefusesOverwriteBeforeReadingToken(t *testing.T) {
	app, registry, _, _, stdout, _ := testApp(t)
	registry.profiles["work"] = testProfile("work")
	input := &spyReader{value: cliTokenSentinel + "\n"}
	app.stdin = input
	code := runApp(app, "auth", "login", "--profile", "work", "--site", "https://tenant.atlassian.net", "--email", "user@example.com", "--token-stdin", "-o", "json")
	if code != errx.CodeConfirm || input.read {
		t.Fatalf("code=%d tokenRead=%v output=%s", code, input.read, stdout.String())
	}
}

func TestAuthLoginVerifiesBeforePersistenceAndNeverPrintsToken(t *testing.T) {
	app, registry, store, reader, stdout, stderr := testApp(t)
	app.stdin = strings.NewReader(cliTokenSentinel + "\n")
	code := runApp(app, "auth", "login", "--profile", "work", "--site", "https://tenant.atlassian.net", "--email", "user@example.com", "--token-stdin", "-o", "raw")
	if code != errx.CodeOK || reader.verifyCalls != 1 {
		t.Fatalf("code=%d verify=%d output=%s stderr=%s", code, reader.verifyCalls, stdout.String(), stderr.String())
	}
	stored := registry.profiles["work"]
	if stored.CloudID != "cloud-id" || stored.CredentialGeneration == "" ||
		!stored.HasCapability(profile.CapabilityRead) || store.credentials["work"].Token != cliTokenSentinel {
		t.Fatal("verified login did not persist both boundaries")
	}
	view := decode(t, stdout)
	if view["credential_generation"] != stored.CredentialGeneration {
		t.Fatalf("rendered credential generation = %v, stored = %q", view["credential_generation"], stored.CredentialGeneration)
	}
	if strings.Contains(stdout.String(), cliTokenSentinel) || strings.Contains(stderr.String(), cliTokenSentinel) {
		t.Fatal("login output exposed the token")
	}
}

func TestAuthLoginFailureRedactsSentinelEverywhere(t *testing.T) {
	app, _, _, reader, stdout, stderr := testApp(t)
	reader.verifyErr = errors.New("verification failed with " + cliTokenSentinel)
	app.stdin = strings.NewReader(cliTokenSentinel + "\n")
	code := runApp(app, "auth", "login", "--profile", "work", "--site", "https://tenant.atlassian.net", "--email", "user@example.com", "--token-stdin", "--verbose", "-o", "json")
	if code != errx.CodeInternal {
		t.Fatalf("code=%d output=%s", code, stdout.String())
	}
	if strings.Contains(stdout.String(), cliTokenSentinel) || strings.Contains(stderr.String(), cliTokenSentinel) {
		t.Fatal("failure output exposed the token")
	}
}

func TestAuthStatusDistinguishesLocalRecoveryStates(t *testing.T) {
	tests := []struct {
		name       string
		metadata   bool
		credential bool
		wantState  string
		wantCode   errx.Code
	}{
		{"ready", true, true, "ready", errx.CodeOK},
		{"metadata only", true, false, "metadata_only", errx.CodeOK},
		{"orphaned credential", false, true, "orphaned_credential", errx.CodeOK},
		{"absent", false, false, "", errx.CodeNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app, registry, store, _, stdout, _ := testApp(t)
			if test.metadata {
				registry.profiles["work"] = testProfile("work")
			}
			if test.credential {
				store.credentials["work"] = auth.Credential{Token: "stored-token"}
			}
			if code := runApp(app, "auth", "status", "--profile", "work", "-o", "json"); code != test.wantCode {
				t.Fatalf("code=%d want=%d output=%s", code, test.wantCode, stdout.String())
			}
			if test.wantState != "" {
				data := decode(t, stdout)["data"].(map[string]any)
				if data["credential_state"] != test.wantState {
					t.Fatalf("state=%v want=%s", data["credential_state"], test.wantState)
				}
			}
		})
	}
}

func TestAuthStatusCheckExplicitlyVerifiesReads(t *testing.T) {
	app, registry, store, reader, stdout, stderr := testApp(t)
	selected := modernWriteProfile("work")
	registry.profiles["work"] = selected
	store.credentials["work"] = auth.Credential{
		Token: "stored-token", ProfileIdentity: profile.CredentialIdentity(selected), Generation: selected.CredentialGeneration,
		Capabilities: append([]profile.Capability(nil), selected.Capabilities...),
	}
	if code := runApp(app, "auth", "status", "--profile", "work", "--check", "-o", "json"); code != errx.CodeOK {
		t.Fatalf("code=%d output=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if reader.verifyCalls != 1 || store.loadCalls != 1 {
		t.Fatalf("verifyCalls=%d loadCalls=%d", reader.verifyCalls, store.loadCalls)
	}
	if got := decode(t, stdout)["data"].(map[string]any)["credential_state"]; got != "verified_reads_write_declared" {
		t.Fatalf("state=%v", got)
	}
}

func TestAuthStatusCheckAttributesVerificationToLockedProfile(t *testing.T) {
	app, registry, store, reader, stdout, _ := testApp(t)
	checked := modernWriteProfile("work")
	registry.profiles["work"] = checked
	store.credentials["work"] = auth.Credential{
		Token: "stored-token", ProfileIdentity: profile.CredentialIdentity(checked), Generation: checked.CredentialGeneration,
		Capabilities: append([]profile.Capability(nil), checked.Capabilities...),
	}
	replacement := modernWriteProfile("work")
	replacement.CredentialGeneration = strings.Repeat("A", 42) + "Q"
	reader.verifyHook = func() { registry.profiles["work"] = replacement }
	if code := runApp(app, "auth", "status", "--profile", "work", "--check", "-o", "raw"); code != errx.CodeOK {
		t.Fatalf("code=%d output=%s", code, stdout.String())
	}
	view := decode(t, stdout)
	if view["credential_generation"] != checked.CredentialGeneration || view["credential_state"] != "verified_reads_write_declared" {
		t.Fatalf("verified status was attributed to another profile: %v", view)
	}
}

func TestAuthStatusCheckPreservesLocalRecoveryStates(t *testing.T) {
	tests := []struct {
		name       string
		metadata   bool
		credential bool
		expired    bool
		wantState  string
		wantCode   errx.Code
	}{
		{name: "metadata only", metadata: true, wantState: "metadata_only", wantCode: errx.CodeOK},
		{name: "orphaned credential", credential: true, wantState: "orphaned_credential", wantCode: errx.CodeOK},
		{name: "expired", metadata: true, credential: true, expired: true, wantState: "expired", wantCode: errx.CodeOK},
		{name: "absent", wantCode: errx.CodeNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app, registry, store, reader, stdout, _ := testApp(t)
			if test.metadata {
				selected := testProfile("work")
				if test.expired {
					expires := time.Now().Add(-24 * time.Hour)
					selected.ExpiresAt = &expires
				}
				registry.profiles["work"] = selected
			}
			if test.credential {
				store.credentials["work"] = auth.Credential{Token: "stored-token"}
			}
			code := runApp(app, "auth", "status", "--profile", "work", "--check", "-o", "json")
			if code != test.wantCode || reader.verifyCalls != 0 || store.loadCalls != 0 {
				t.Fatalf("code=%d want=%d verify=%d load=%d output=%s", code, test.wantCode, reader.verifyCalls, store.loadCalls, stdout.String())
			}
			if test.wantState != "" && decode(t, stdout)["data"].(map[string]any)["credential_state"] != test.wantState {
				t.Fatalf("output=%s", stdout.String())
			}
		})
	}
}

func TestAuthMigrateKeychainDryRunAndApply(t *testing.T) {
	app, registry, store, _, stdout, _ := testApp(t)
	registry.profiles["work"] = testProfile("work")
	store.credentials["work"] = auth.Credential{Token: "stored-token"}
	if code := runApp(app, "auth", "migrate-keychain", "--profile", "work", "--dry-run", "-o", "json"); code != errx.CodeOK {
		t.Fatalf("dry-run code=%d output=%s", code, stdout.String())
	}
	if store.migrateCalls != 0 || store.loadCalls != 0 {
		t.Fatalf("dry-run touched credential material: migrate=%d load=%d", store.migrateCalls, store.loadCalls)
	}
	stdout.Reset()
	if code := runApp(app, "auth", "migrate-keychain", "--profile", "work", "--yes", "-o", "json"); code != errx.CodeOK {
		t.Fatalf("apply code=%d output=%s", code, stdout.String())
	}
	if store.migrateCalls != 1 || store.loadCalls != 0 {
		t.Fatalf("migration calls=%d load calls=%d", store.migrateCalls, store.loadCalls)
	}
}

func TestNetworkCommandEmitsBoundedMachineEnvelope(t *testing.T) {
	app, registry, store, reader, stdout, stderr := testApp(t)
	registry.profiles["work"] = testProfile("work")
	store.credentials["work"] = auth.Credential{Token: "stored-token"}
	reader.spaces = confluence.PageResult[confluence.Spaces]{
		Results: confluence.Spaces{{ID: "1", Key: "ENG", Name: "Engineering", Status: "current"}}, NextCursor: "opaque-next",
	}
	code := runApp(app, "spaces", "list", "--profile", "work", "--limit", "1", "-o", "json")
	if code != errx.CodeOK || stderr.Len() != 0 {
		t.Fatalf("code=%d output=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	envelope := decode(t, stdout)
	meta := envelope["meta"].(map[string]any)
	if meta["profile"] != "work" || meta["next_cursor"] != "opaque-next" || meta["count"] != float64(1) {
		t.Fatalf("meta=%v", meta)
	}
}

func TestReadRejectsExpiryOnlyCredentialBindingMutationBeforeClientOrNetwork(t *testing.T) {
	app, registry, store, reader, stdout, stderr := testApp(t)
	bound := modernWriteProfile("work")
	store.credentials["work"] = auth.Credential{
		Token: cliTokenSentinel, ProfileIdentity: profile.CredentialIdentity(bound), Generation: bound.CredentialGeneration,
		Capabilities: append([]profile.Capability(nil), bound.Capabilities...),
	}
	now := time.Date(2035, time.June, 7, 8, 9, 10, 0, time.UTC)
	expiresAt := now.Add(24 * time.Hour)
	selected := bound
	selected.ExpiresAt = &expiresAt
	registry.profiles["work"] = selected
	app.now = func() time.Time { return now }
	clientCalls := 0
	app.newClient = func(profile.Profile, auth.Credential, *slog.Logger) (confluenceReader, error) {
		clientCalls++
		return reader, nil
	}

	code := runApp(app, "spaces", "list", "--profile", "work", "-o", "json")
	if code != errx.CodeAuth {
		t.Fatalf("code=%d output=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if got := decode(t, stdout)["error"].(map[string]any)["code"]; got != "CREDENTIAL_BINDING_MISMATCH" {
		t.Fatalf("error code=%v output=%s", got, stdout.String())
	}
	if store.loadCalls != 1 || clientCalls != 0 || reader.spacesCalls != 0 {
		t.Fatalf("load=%d client=%d network=%d", store.loadCalls, clientCalls, reader.spacesCalls)
	}
	if strings.Contains(stdout.String(), cliTokenSentinel) || strings.Contains(stderr.String(), cliTokenSentinel) {
		t.Fatal("credential binding failure exposed the token")
	}
}
