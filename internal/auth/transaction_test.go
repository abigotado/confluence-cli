package auth

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abigotado/confluence-cli/internal/profile"
)

type fakeStore struct {
	credentials     map[string]Credential
	migrateErr      error
	saveErr         error
	rollbackSaveErr error
	saveCalls       int
	deleteErr       error
	operations      []string
}

func (f *fakeStore) MigrateKeychain(_ context.Context, name string) error {
	f.operations = append(f.operations, "migrate:"+name)
	return f.migrateErr
}

func (f *fakeStore) Exists(_ context.Context, name string) (bool, error) {
	_, ok := f.credentials[name]
	return ok, nil
}

func (f *fakeStore) Load(_ context.Context, name string) (Credential, error) {
	f.operations = append(f.operations, "load:"+name)
	credential, ok := f.credentials[name]
	if !ok {
		return Credential{}, ErrNotFound
	}
	return credential, nil
}

func (f *fakeStore) Save(_ context.Context, name string, credential Credential) error {
	f.operations = append(f.operations, "save:"+name)
	f.saveCalls++
	if f.saveCalls > 1 && f.rollbackSaveErr != nil {
		return f.rollbackSaveErr
	}
	if f.saveErr != nil {
		return f.saveErr
	}
	f.credentials[name] = credential
	return nil
}

func TestLoginRollbackFailureLeavesCredentialBindingMismatch(t *testing.T) {
	previous, err := authProfile("work").WithNewCredentialGeneration()
	if err != nil {
		t.Fatal(err)
	}
	previousCredential := Credential{
		Token: "old-token", ProfileIdentity: profile.CredentialIdentity(previous), Generation: previous.CredentialGeneration,
		Capabilities: append([]profile.Capability(nil), previous.Capabilities...),
	}
	store := &fakeStore{
		credentials:     map[string]Credential{"work": previousCredential},
		rollbackSaveErr: errors.New("rollback Keychain save failed"),
	}
	registry := &fakeRegistry{
		profiles: map[string]profile.Profile{"work": previous},
		putErr:   errors.New("profile registry write failed before commit"),
	}
	if _, err := Login(context.Background(), store, registry, authProfile("work"), Credential{Token: "new-token"}, true); err == nil {
		t.Fatal("Login() error = nil, want joined persistence and rollback failure")
	}
	remaining := store.credentials["work"]
	if remaining.Token != "new-token" || remaining.Generation == previous.CredentialGeneration {
		t.Fatalf("remaining credential does not model failed rollback: %#v", remaining)
	}
	if err := ValidateCredentialBinding(remaining, registry.profiles["work"]); !errors.Is(err, ErrCredentialBindingMismatch) {
		t.Fatalf("binding error = %v, want fail-closed mismatch", err)
	}
}

func (f *fakeStore) Delete(_ context.Context, name string) error {
	f.operations = append(f.operations, "delete:"+name)
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.credentials, name)
	return nil
}

type fakeRegistry struct {
	mu            sync.Mutex
	profiles      map[string]profile.Profile
	addErr        error
	putErr        error
	putCommits    bool
	removeErr     error
	removeCommits bool
	operations    []string
}

func (f *fakeRegistry) WithProfileLock(_ context.Context, _ string, fn func() error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fn()
}

func (f *fakeRegistry) Get(_ context.Context, name string) (profile.Profile, error) {
	f.operations = append(f.operations, "get:"+name)
	p, ok := f.profiles[name]
	if !ok {
		return profile.Profile{}, profile.ErrNotFound
	}
	return p, nil
}

func (f *fakeRegistry) Add(_ context.Context, p profile.Profile) error {
	f.operations = append(f.operations, "add:"+p.Name)
	if f.addErr != nil {
		return f.addErr
	}
	f.profiles[p.Name] = p
	return nil
}

func (f *fakeRegistry) Put(_ context.Context, p profile.Profile) error {
	f.operations = append(f.operations, "put:"+p.Name)
	if f.putCommits {
		f.profiles[p.Name] = p
	}
	if f.putErr != nil {
		return f.putErr
	}
	f.profiles[p.Name] = p
	return nil
}

func (f *fakeRegistry) Remove(_ context.Context, name string) error {
	f.operations = append(f.operations, "remove:"+name)
	if f.removeCommits {
		delete(f.profiles, name)
	}
	if f.removeErr != nil {
		return f.removeErr
	}
	delete(f.profiles, name)
	return nil
}

func authProfile(name string) profile.Profile {
	return profile.Profile{Name: name, Site: "https://tenant.atlassian.net", Email: "user@example.com", CloudID: "cloud-id"}
}

func TestLoginTransaction(t *testing.T) {
	tests := []struct {
		name               string
		existingProfile    bool
		existingCredential bool
		overwrite          bool
		addErr             error
		putErr             error
		existingDifferent  bool
		wantErr            error
		wantToken          string
		wantStoreOps       []string
	}{
		{
			name:         "new login saves credential before registry",
			wantToken:    "new-token",
			wantStoreOps: []string{"save:work"},
		},
		{
			name:               "overwrite requires confirmation",
			existingProfile:    true,
			existingCredential: true,
			wantErr:            ErrOverwriteConfirmationRequired,
			wantToken:          "old-token",
			wantStoreOps:       nil,
		},
		{
			name:               "confirmed overwrite succeeds",
			existingProfile:    true,
			existingCredential: true,
			overwrite:          true,
			wantToken:          "new-token",
			wantStoreOps:       []string{"migrate:work", "load:work", "save:work"},
		},
		{
			name:         "new login compensates registry failure with delete",
			addErr:       errors.New("registry unavailable"),
			wantErr:      errors.New("any"),
			wantStoreOps: []string{"save:work", "delete:work"},
		},
		{
			name:               "overwrite restores previous credential when registry update fails",
			existingProfile:    true,
			existingCredential: true,
			overwrite:          true,
			putErr:             errors.New("registry unavailable"),
			existingDifferent:  true,
			wantErr:            errors.New("any"),
			wantToken:          "old-token",
			wantStoreOps:       []string{"migrate:work", "load:work", "save:work", "save:work"},
		},
		{
			name:               "orphan credential also requires confirmation",
			existingCredential: true,
			wantErr:            ErrOverwriteConfirmationRequired,
			wantToken:          "old-token",
			wantStoreOps:       nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{credentials: map[string]Credential{}}
			registry := &fakeRegistry{profiles: map[string]profile.Profile{}, addErr: tt.addErr, putErr: tt.putErr}
			if tt.existingProfile {
				existing := authProfile("work")
				if tt.existingDifferent {
					existing.Email = "old@example.com"
				}
				registry.profiles["work"] = existing
			}
			if tt.existingCredential {
				store.credentials["work"] = Credential{Token: "old-token"}
			}
			_, err := Login(context.Background(), store, registry, authProfile("work"), Credential{Token: "new-token"}, tt.overwrite)
			if tt.wantErr == nil && err != nil {
				t.Fatalf("Login() error = %v", err)
			}
			if tt.wantErr != nil && err == nil {
				t.Fatal("Login() error = nil, want an error")
			}
			if errors.Is(tt.wantErr, ErrOverwriteConfirmationRequired) && !errors.Is(err, ErrOverwriteConfirmationRequired) {
				t.Fatalf("Login() error = %v, want ErrOverwriteConfirmationRequired", err)
			}
			if got := store.credentials["work"].Token; got != tt.wantToken {
				t.Fatalf("stored token = %q, want %q", got, tt.wantToken)
			}
			if len(store.operations) != len(tt.wantStoreOps) {
				t.Fatalf("store operations = %v, want %v", store.operations, tt.wantStoreOps)
			}
			for i := range tt.wantStoreOps {
				if store.operations[i] != tt.wantStoreOps[i] {
					t.Fatalf("store operations = %v, want %v", store.operations, tt.wantStoreOps)
				}
			}
		})
	}
}

func TestLoginReplacesCredentialGeneration(t *testing.T) {
	store := &fakeStore{credentials: map[string]Credential{}}
	registry := &fakeRegistry{profiles: map[string]profile.Profile{}}
	intent := authProfile("work")
	if _, err := Login(context.Background(), store, registry, intent, Credential{Token: "first-token"}, false); err != nil {
		t.Fatalf("first Login() error = %v", err)
	}
	first := registry.profiles["work"]
	if err := first.Validate(); err != nil || first.CredentialGeneration == "" {
		t.Fatalf("first persisted profile = %#v, validation error = %v", first, err)
	}
	if !first.HasCapability(profile.CapabilityRead) || first.HasCapability(profile.CapabilityPageWrite) {
		t.Fatalf("default capabilities = %v, want read only", first.Capabilities)
	}
	if _, err := Login(context.Background(), store, registry, intent, Credential{Token: "second-token"}, true); err != nil {
		t.Fatalf("second Login() error = %v", err)
	}
	second := registry.profiles["work"]
	if second.CredentialGeneration == first.CredentialGeneration {
		t.Fatalf("credential generation was reused: %q", second.CredentialGeneration)
	}
}

func TestLoginRejectsCallerSuppliedCredentialGeneration(t *testing.T) {
	store := &fakeStore{credentials: map[string]Credential{}}
	registry := &fakeRegistry{profiles: map[string]profile.Profile{}}
	intent := authProfile("work")
	intent.CredentialGeneration = "caller-controlled"
	_, err := Login(context.Background(), store, registry, intent, Credential{Token: "new-token"}, false)
	if !errors.Is(err, profile.ErrInvalidProfile) {
		t.Fatalf("Login() error = %v, want ErrInvalidProfile", err)
	}
	if len(store.operations) != 0 || len(registry.profiles) != 0 {
		t.Fatalf("invalid login mutated store=%v registry=%v", store.operations, registry.profiles)
	}
}

func TestLoginStopsBeforeCredentialReadAfterMigrationFailure(t *testing.T) {
	migrationFailure := errors.New("ACL migration failed")
	store := &fakeStore{
		credentials: map[string]Credential{"work": {Token: "old-token"}},
		migrateErr:  migrationFailure,
	}
	registry := &fakeRegistry{profiles: map[string]profile.Profile{"work": authProfile("work")}}
	_, err := Login(context.Background(), store, registry, authProfile("work"), Credential{Token: "new-token"}, true)
	if !errors.Is(err, migrationFailure) {
		t.Fatalf("Login() error = %v, want migration failure", err)
	}
	if got := store.operations; len(got) != 1 || got[0] != "migrate:work" {
		t.Fatalf("store operations = %v, want migration before credential access", got)
	}
	if got := store.credentials["work"].Token; got != "old-token" {
		t.Fatalf("credential changed after migration failure: %q", got)
	}
}

func TestLoginMetadataFailureKeepsPreviousCredentialGeneration(t *testing.T) {
	previous, err := authProfile("work").WithNewCredentialGeneration()
	if err != nil {
		t.Fatalf("prepare previous profile: %v", err)
	}
	store := &fakeStore{credentials: map[string]Credential{"work": {Token: "old-token"}}}
	registry := &fakeRegistry{
		profiles: map[string]profile.Profile{"work": previous},
		putErr:   errors.New("registry unavailable before rename"),
	}
	_, err = Login(context.Background(), store, registry, authProfile("work"), Credential{Token: "new-token"}, true)
	if err == nil {
		t.Fatal("Login() error = nil")
	}
	if got := registry.profiles["work"].CredentialGeneration; got != previous.CredentialGeneration {
		t.Fatalf("failed login published generation %q, want previous %q", got, previous.CredentialGeneration)
	}
	if got := store.credentials["work"].Token; got != "old-token" {
		t.Fatalf("failed login did not restore previous credential: %q", got)
	}
}

type migrationStore struct {
	exists     bool
	existsErr  error
	migrateErr error
	operations []string
}

func (s *migrationStore) Exists(_ context.Context, name string) (bool, error) {
	s.operations = append(s.operations, "exists:"+name)
	return s.exists, s.existsErr
}

func (*migrationStore) Load(context.Context, string) (Credential, error) {
	panic("MigrateKeychain must not load credentials")
}

func (*migrationStore) Save(context.Context, string, Credential) error {
	panic("MigrateKeychain must not save credentials")
}

func (*migrationStore) Delete(context.Context, string) error {
	panic("MigrateKeychain must not delete credentials")
}

func (s *migrationStore) MigrateKeychain(_ context.Context, name string) error {
	s.operations = append(s.operations, "migrate:"+name)
	return s.migrateErr
}

func TestMigrateKeychainOrchestration(t *testing.T) {
	tests := []struct {
		name       string
		registered bool
		exists     bool
		existsErr  error
		migrateErr error
		wantErr    error
		wantOps    []string
	}{
		{name: "registered exact profile migrates without credential access", registered: true, exists: true, wantOps: []string{"exists:work", "migrate:work"}},
		{name: "unknown profile stops before Keychain", wantErr: profile.ErrNotFound},
		{name: "missing exact entry is typed", registered: true, wantErr: ErrKeychainEntryNotFound, wantOps: []string{"exists:work"}},
		{name: "blocked existence check is typed", registered: true, existsErr: ErrInteractionNotAllowed, wantErr: ErrKeychainMigrationBlocked, wantOps: []string{"exists:work"}},
		{name: "unsupported migration is typed", registered: true, exists: true, migrateErr: ErrUnsupported, wantErr: ErrKeychainMigrationUnavailable, wantOps: []string{"exists:work", "migrate:work"}},
		{name: "canceled migration remains distinct", registered: true, exists: true, migrateErr: ErrKeychainMigrationCanceled, wantErr: ErrKeychainMigrationCanceled, wantOps: []string{"exists:work", "migrate:work"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &migrationStore{exists: tt.exists, existsErr: tt.existsErr, migrateErr: tt.migrateErr}
			profiles := map[string]profile.Profile{}
			if tt.registered {
				profiles["work"] = authProfile("work")
			}
			registry := &fakeRegistry{profiles: profiles}
			err := MigrateKeychain(context.Background(), store, registry, "work")
			if tt.wantErr == nil && err != nil {
				t.Fatalf("MigrateKeychain() error = %v", err)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("MigrateKeychain() error = %v, want %v", err, tt.wantErr)
			}
			if len(store.operations) != len(tt.wantOps) {
				t.Fatalf("store operations = %v, want %v", store.operations, tt.wantOps)
			}
			for i := range tt.wantOps {
				if store.operations[i] != tt.wantOps[i] {
					t.Fatalf("store operations = %v, want %v", store.operations, tt.wantOps)
				}
			}
		})
	}
}

func TestLogoutDeletesCredentialBeforeRegistry(t *testing.T) {
	store := &fakeStore{credentials: map[string]Credential{"work": {Token: "old-token"}}}
	registry := &fakeRegistry{profiles: map[string]profile.Profile{"work": authProfile("work")}}
	if err := Logout(context.Background(), store, registry, "work"); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if len(store.credentials) != 0 || len(registry.profiles) != 0 {
		t.Fatalf("Logout() left store=%d registry=%d entries", len(store.credentials), len(registry.profiles))
	}
	if len(store.operations) != 2 || store.operations[0] != "load:work" || store.operations[1] != "delete:work" {
		t.Fatalf("store operations = %v", store.operations)
	}
	if len(registry.operations) != 1 || registry.operations[0] != "remove:work" {
		t.Fatalf("registry operations = %v", registry.operations)
	}
}

func TestConcurrentLoginsSerializeCompleteProfileTransaction(t *testing.T) {
	store := &gatedStore{
		credentials: map[string]Credential{},
		firstSave:   make(chan struct{}),
		releaseSave: make(chan struct{}),
	}
	registry := &fakeRegistry{profiles: map[string]profile.Profile{}}
	type loginOutcome struct {
		profile profile.Profile
		err     error
	}
	firstDone := make(chan loginOutcome, 1)
	go func() {
		stored, err := Login(context.Background(), store, registry, authProfile("work"), Credential{Token: "first-token"}, true)
		firstDone <- loginOutcome{profile: stored, err: err}
	}()
	<-store.firstSave

	secondDone := make(chan loginOutcome, 1)
	go func() {
		stored, err := Login(context.Background(), store, registry, authProfile("work"), Credential{Token: "second-token"}, true)
		secondDone <- loginOutcome{profile: stored, err: err}
	}()
	time.Sleep(30 * time.Millisecond)
	if loads := store.loads.Load(); loads != 1 {
		t.Fatalf("second login crossed the profile lock: loads=%d", loads)
	}
	close(store.releaseSave)
	first := <-firstDone
	if first.err != nil {
		t.Fatalf("first login: %v", first.err)
	}
	second := <-secondDone
	if second.err != nil {
		t.Fatalf("second login: %v", second.err)
	}
	if first.profile.CredentialGeneration == "" || second.profile.CredentialGeneration == "" ||
		first.profile.CredentialGeneration == second.profile.CredentialGeneration {
		t.Fatalf("login results do not identify their transactions: first=%q second=%q", first.profile.CredentialGeneration, second.profile.CredentialGeneration)
	}
	if got := registry.profiles["work"].CredentialGeneration; got != second.profile.CredentialGeneration {
		t.Fatalf("final profile generation = %q, second login returned %q", got, second.profile.CredentialGeneration)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if got := store.credentials["work"].Token; got != "second-token" {
		t.Fatalf("final token = %q, want second-token", got)
	}
}

func TestLoginDoesNotRollbackCredentialAfterMetadataCommitted(t *testing.T) {
	store := &fakeStore{credentials: map[string]Credential{"work": {Token: "old-token"}}}
	existing := authProfile("work")
	existing.Email = "old@example.com"
	registry := &fakeRegistry{
		profiles:   map[string]profile.Profile{"work": existing},
		putErr:     &profile.CommitError{Err: errors.New("directory sync failed after rename")},
		putCommits: true,
	}
	_, err := Login(context.Background(), store, registry, authProfile("work"), Credential{Token: "new-token"}, true)
	if err == nil {
		t.Fatal("durability failure was not reported")
	}
	if got := store.credentials["work"].Token; got != "new-token" {
		t.Fatalf("credential was rolled back after metadata commit: %q", got)
	}
	if got := registry.profiles["work"].Email; got != "user@example.com" {
		t.Fatalf("committed metadata = %q", got)
	}
}

func TestLoginRetainsCredentialAfterCommittedMetadataWithoutVerificationRead(t *testing.T) {
	store := &fakeStore{credentials: map[string]Credential{"work": {Token: "old-token"}}}
	existing := authProfile("work")
	existing.Email = "old@example.com"
	registry := &fakeRegistry{
		profiles:   map[string]profile.Profile{"work": existing},
		putErr:     &profile.CommitError{Err: errors.New("directory sync failed after rename")},
		putCommits: true,
	}
	_, err := Login(context.Background(), store, registry, authProfile("work"), Credential{Token: "new-token"}, true)
	if err == nil || !profile.WasCommitted(err) {
		t.Fatalf("Login() error = %v, want committed durability error", err)
	}
	if got := store.credentials["work"].Token; got != "new-token" {
		t.Fatalf("credential was rolled back after committed metadata: %q", got)
	}
	if got := registry.operations; len(got) != 2 || got[0] != "get:work" || got[1] != "put:work" {
		t.Fatalf("registry operations = %v, want no verification read", got)
	}
}

func TestLogoutRestoresCredentialAfterPreCommitMetadataFailure(t *testing.T) {
	store := &fakeStore{credentials: map[string]Credential{"work": {Token: "old-token"}}}
	registry := &fakeRegistry{
		profiles:  map[string]profile.Profile{"work": authProfile("work")},
		removeErr: errors.New("registry unavailable before rename"),
	}
	err := Logout(context.Background(), store, registry, "work")
	if err == nil {
		t.Fatal("Logout() error = nil")
	}
	if got := store.credentials["work"].Token; got != "old-token" {
		t.Fatalf("credential was not restored: %q", got)
	}
	if _, ok := registry.profiles["work"]; !ok {
		t.Fatal("pre-commit failure removed profile metadata")
	}
}

func TestLogoutKeepsCredentialDeletedAfterCommittedMetadataRemoval(t *testing.T) {
	store := &fakeStore{credentials: map[string]Credential{"work": {Token: "old-token"}}}
	registry := &fakeRegistry{
		profiles:      map[string]profile.Profile{"work": authProfile("work")},
		removeErr:     &profile.CommitError{Err: errors.New("directory sync failed after rename")},
		removeCommits: true,
	}
	err := Logout(context.Background(), store, registry, "work")
	if err == nil || !profile.WasCommitted(err) {
		t.Fatalf("Logout() error = %v, want committed durability error", err)
	}
	if _, ok := store.credentials["work"]; ok {
		t.Fatal("credential was restored after committed metadata removal")
	}
	if _, ok := registry.profiles["work"]; ok {
		t.Fatal("committed removal left profile metadata")
	}
}

func TestLogoutRemovesOrphanCredentialWhenMetadataIsAbsent(t *testing.T) {
	store := &fakeStore{credentials: map[string]Credential{"work": {Token: "orphan-token"}}}
	registry := &fakeRegistry{profiles: map[string]profile.Profile{}}
	if err := Logout(context.Background(), store, registry, "work"); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if _, ok := store.credentials["work"]; ok {
		t.Fatal("orphan credential remains")
	}
}

type gatedStore struct {
	mu          sync.Mutex
	credentials map[string]Credential
	loads       atomic.Int32
	firstSave   chan struct{}
	releaseSave chan struct{}
	saveOnce    sync.Once
}

func (*gatedStore) MigrateKeychain(context.Context, string) error {
	return nil
}

func (s *gatedStore) Exists(_ context.Context, name string) (bool, error) {
	s.loads.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.credentials[name]
	return ok, nil
}

func (s *gatedStore) Load(_ context.Context, name string) (Credential, error) {
	s.loads.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	credential, ok := s.credentials[name]
	if !ok {
		return Credential{}, ErrNotFound
	}
	return credential, nil
}

func (s *gatedStore) Save(_ context.Context, name string, credential Credential) error {
	blocked := false
	s.saveOnce.Do(func() {
		blocked = true
		close(s.firstSave)
	})
	if blocked {
		<-s.releaseSave
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.credentials[name] = credential
	return nil
}

func (s *gatedStore) Delete(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.credentials, name)
	return nil
}
