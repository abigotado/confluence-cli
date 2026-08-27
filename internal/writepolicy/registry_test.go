package writepolicy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abigotado/confluence-cli/internal/lockfile"
	"github.com/abigotado/confluence-cli/internal/profile"
)

const testGeneration = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func testProfile(name string) profile.Profile {
	return profile.Profile{
		Name:                 name,
		Site:                 "https://example.atlassian.net",
		Email:                name + "@example.invalid",
		CloudID:              "cloud-" + name,
		CredentialGeneration: testGeneration,
		Capabilities:         []profile.Capability{profile.CapabilityRead, profile.CapabilityPageWrite},
	}
}

func TestIdentityForUsesEveryCanonicalNonSecretField(t *testing.T) {
	value := testProfile("work")
	if got, want := IdentityFor(value), "ce3fd261322f854fe6139e35670c8b189b47458e04692ec8de954f1e2146773e"; got != want {
		t.Fatalf("IdentityFor() = %q, want %q", got, want)
	}
	uppercaseEmail := value
	uppercaseEmail.Email = "WORK@example.invalid"
	if got, want := IdentityFor(uppercaseEmail), IdentityFor(value); got != want {
		t.Fatalf("uppercase email identity = %q, want canonical %q", got, want)
	}

	tests := []struct {
		name   string
		change func(profile.Profile) profile.Profile
	}{
		{name: "profile name", change: func(p profile.Profile) profile.Profile { p.Name = "other"; return p }},
		{name: "site", change: func(p profile.Profile) profile.Profile { p.Site = "https://other.atlassian.net"; return p }},
		{name: "email", change: func(p profile.Profile) profile.Profile { p.Email = "other@example.invalid"; return p }},
		{name: "cloud ID", change: func(p profile.Profile) profile.Profile { p.CloudID = "cloud-other"; return p }},
		{name: "credential generation", change: func(p profile.Profile) profile.Profile {
			p.CredentialGeneration = "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE"
			return p
		}},
		{name: "capabilities", change: func(p profile.Profile) profile.Profile {
			p.Capabilities = []profile.Capability{profile.CapabilityRead}
			return p
		}},
	}
	for _, test := range tests {
		t.Run(test.name+" changes binding", func(t *testing.T) {
			if got := IdentityFor(test.change(value)); got == IdentityFor(value) {
				t.Fatalf("IdentityFor() did not change from %q", got)
			}
		})
	}
}

func TestCanonicalSpaces(t *testing.T) {
	tooMany := make([]string, maxSpaces+1)
	for index := range tooMany {
		tooMany[index] = "1"
	}
	tests := []struct {
		name    string
		spaces  []string
		want    []string
		wantErr bool
	}{
		{name: "trims sorts and deduplicates", spaces: []string{" 42 ", "2", "10", "42"}, want: []string{"10", "2", "42"}},
		{name: "empty is rejected", wantErr: true},
		{name: "zero is rejected", spaces: []string{"0"}, wantErr: true},
		{name: "leading zero is rejected", spaces: []string{"042"}, wantErr: true},
		{name: "negative is rejected", spaces: []string{"-1"}, wantErr: true},
		{name: "non digit is rejected", spaces: []string{"12a"}, wantErr: true},
		{name: "overlong ID is rejected", spaces: []string{strings.Repeat("1", maxSpaceIDLength+1)}, wantErr: true},
		{name: "too many inputs are rejected before deduplication", spaces: tooMany, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := CanonicalSpaces(test.spaces)
			if test.wantErr {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("CanonicalSpaces() error = %v, want ErrInvalid", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("CanonicalSpaces() error = %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("spaces = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestRegistryPersistsStrictIdentityBoundPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "write-policies.json")
	registry := NewRegistry(path)
	value := testProfile("work")

	policy, err := registry.Set(context.Background(), value, []string{"42", "10", "42"})
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if want := []string{"10", "42"}; !reflect.DeepEqual(policy.Spaces, want) {
		t.Fatalf("spaces = %#v, want %#v", policy.Spaces, want)
	}
	if policy.Identity != IdentityFor(value) {
		t.Fatalf("identity = %q, want %q", policy.Identity, IdentityFor(value))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %04o, want 0600", got)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{value.Site, value.Email, value.CloudID, value.CredentialGeneration, "token", "page body"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("registry contains forbidden value %q", forbidden)
		}
	}
	if got, err := registry.GetBound(context.Background(), value); err != nil || got.Profile != "work" {
		t.Fatalf("GetBound() = %#v, %v", got, err)
	}
	if got, err := registry.RequireSpace(context.Background(), value, "42"); err != nil || got.Profile != "work" {
		t.Fatalf("RequireSpace() = %#v, %v", got, err)
	}
	if _, err := registry.RequireSpace(context.Background(), value, "43"); !errors.Is(err, ErrSpaceDenied) {
		t.Fatalf("denied RequireSpace() error = %v, want ErrSpaceDenied", err)
	}
	if _, err := registry.RequireSpace(context.Background(), value, "042"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("noncanonical RequireSpace() error = %v, want ErrInvalid", err)
	}

	staleProfiles := []struct {
		name   string
		change func(profile.Profile) profile.Profile
	}{
		{name: "email", change: func(p profile.Profile) profile.Profile { p.Email = "other@example.invalid"; return p }},
		{name: "site", change: func(p profile.Profile) profile.Profile { p.Site = "https://other.atlassian.net"; return p }},
		{name: "cloud ID", change: func(p profile.Profile) profile.Profile { p.CloudID = "cloud-other"; return p }},
		{name: "credential generation", change: func(p profile.Profile) profile.Profile {
			p.CredentialGeneration = "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE"
			return p
		}},
		{name: "capabilities", change: func(p profile.Profile) profile.Profile {
			p.Capabilities = []profile.Capability{profile.CapabilityRead}
			return p
		}},
	}
	for _, test := range staleProfiles {
		t.Run("changed "+test.name+" makes policy stale", func(t *testing.T) {
			_, err := registry.GetBound(context.Background(), test.change(value))
			if !errors.Is(err, ErrStale) {
				t.Fatalf("error = %v, want ErrStale", err)
			}
		})
	}

	if err := registry.Clear(context.Background(), value.Name); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
	if err := registry.Clear(context.Background(), value.Name); err != nil {
		t.Fatalf("idempotent Clear() error = %v", err)
	}
	if _, err := registry.Get(context.Background(), value.Name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}

func TestRegistrySetRequiresModernPageWriteProfile(t *testing.T) {
	tests := []struct {
		name  string
		value profile.Profile
	}{
		{name: "legacy profile", value: func() profile.Profile {
			p := testProfile("work")
			p.CredentialGeneration = ""
			p.Capabilities = nil
			return p
		}()},
		{name: "read only profile", value: func() profile.Profile {
			p := testProfile("work")
			p.Capabilities = []profile.Capability{profile.CapabilityRead}
			return p
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := NewRegistry(filepath.Join(t.TempDir(), "config", "write-policies.json"))
			if _, err := registry.Set(context.Background(), test.value, []string{"42"}); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Set() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestRegistryRejectsCorruptOrInsecureFiles(t *testing.T) {
	identity := strings.Repeat("a", 64)
	policy := `{"profile":"work","identity":"` + identity + `","spaces":["42"]}`
	tests := []struct {
		name    string
		body    string
		mode    os.FileMode
		wantErr error
	}{
		{name: "unknown field", body: `{"version":1,"policies":[],"extra":true}`, mode: 0o600, wantErr: ErrCorruptRegistry},
		{name: "trailing JSON", body: `{"version":1,"policies":[]} {}`, mode: 0o600, wantErr: ErrCorruptRegistry},
		{name: "null policies", body: `{"version":1,"policies":null}`, mode: 0o600, wantErr: ErrCorruptRegistry},
		{name: "wrong version", body: `{"version":2,"policies":[]}`, mode: 0o600, wantErr: ErrCorruptRegistry},
		{name: "duplicate profiles", body: `{"version":1,"policies":[` + policy + `,` + policy + `]}`, mode: 0o600, wantErr: ErrCorruptRegistry},
		{name: "unsorted profiles", body: `{"version":1,"policies":[{"profile":"z","identity":"` + identity + `","spaces":["42"]},{"profile":"a","identity":"` + identity + `","spaces":["42"]}]}`, mode: 0o600, wantErr: ErrCorruptRegistry},
		{name: "uppercase identity", body: `{"version":1,"policies":[{"profile":"work","identity":"` + strings.Repeat("A", 64) + `","spaces":["42"]}]}`, mode: 0o600, wantErr: ErrCorruptRegistry},
		{name: "short identity", body: `{"version":1,"policies":[{"profile":"work","identity":"abc","spaces":["42"]}]}`, mode: 0o600, wantErr: ErrCorruptRegistry},
		{name: "empty spaces", body: `{"version":1,"policies":[{"profile":"work","identity":"` + identity + `","spaces":[]}]}`, mode: 0o600, wantErr: ErrCorruptRegistry},
		{name: "null spaces", body: `{"version":1,"policies":[{"profile":"work","identity":"` + identity + `","spaces":null}]}`, mode: 0o600, wantErr: ErrCorruptRegistry},
		{name: "duplicate spaces", body: `{"version":1,"policies":[{"profile":"work","identity":"` + identity + `","spaces":["42","42"]}]}`, mode: 0o600, wantErr: ErrCorruptRegistry},
		{name: "unsorted spaces", body: `{"version":1,"policies":[{"profile":"work","identity":"` + identity + `","spaces":["42","10"]}]}`, mode: 0o600, wantErr: ErrCorruptRegistry},
		{name: "noncanonical space", body: `{"version":1,"policies":[{"profile":"work","identity":"` + identity + `","spaces":["042"]}]}`, mode: 0o600, wantErr: ErrCorruptRegistry},
		{name: "broad file permissions", body: `{"version":1,"policies":[]}`, mode: 0o644, wantErr: ErrInsecurePermissions},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "write-policies.json")
			if err := os.WriteFile(path, []byte(test.body), test.mode); err != nil {
				t.Fatal(err)
			}
			_, err := NewRegistry(path).Get(context.Background(), "work")
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
		})
	}

	t.Run("broad directory permissions", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := NewRegistry(filepath.Join(dir, "write-policies.json")).Get(context.Background(), "work")
		if !errors.Is(err, ErrInsecurePermissions) {
			t.Fatalf("error = %v, want ErrInsecurePermissions", err)
		}
	})

	t.Run("registry symlink", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dir, "target")
		if err := os.WriteFile(target, []byte(`{"version":1,"policies":[]}`), 0o600); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "write-policies.json")
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		_, err := NewRegistry(path).Get(context.Background(), "work")
		if !errors.Is(err, ErrInsecurePermissions) {
			t.Fatalf("error = %v, want ErrInsecurePermissions", err)
		}
	})

	t.Run("oversized registry", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "write-policies.json")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(path, maxRegistryBytes+1); err != nil {
			t.Fatal(err)
		}
		_, err := NewRegistry(path).Get(context.Background(), "work")
		if !errors.Is(err, ErrCorruptRegistry) {
			t.Fatalf("error = %v, want ErrCorruptRegistry", err)
		}
	})

	t.Run("too many policies", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		policies := make([]string, maxPolicies+1)
		for index := range policies {
			policies[index] = policy
		}
		path := filepath.Join(dir, "write-policies.json")
		body := `{"version":1,"policies":[` + strings.Join(policies, ",") + `]}`
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := NewRegistry(path).Get(context.Background(), "work")
		if !errors.Is(err, ErrCorruptRegistry) {
			t.Fatalf("error = %v, want ErrCorruptRegistry", err)
		}
	})
}

func TestRegistryReportsDirectoryOpenFailureAsCommitted(t *testing.T) {
	registry := NewRegistry(filepath.Join(t.TempDir(), "config", "write-policies.json"))
	registry.openDir = func(string) (*os.File, error) {
		return nil, errors.New("injected directory open failure")
	}
	value := testProfile("work")
	_, err := registry.Set(context.Background(), value, []string{"42"})
	if !WasCommitted(err) {
		t.Fatalf("Set() error = %v, want CommitError", err)
	}
	policy, getErr := registry.Get(context.Background(), value.Name)
	if getErr != nil || !reflect.DeepEqual(policy.Spaces, []string{"42"}) {
		t.Fatalf("committed policy = %#v, error = %v", policy, getErr)
	}
}

func TestRegistryRefusesToWriteBeyondReadableSizeLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "write-policies.json")
	registry := NewRegistry(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	err := registry.write(registryFile{
		Version: registryVersion,
		Policies: []Policy{{
			Profile: strings.Repeat("x", maxRegistryBytes),
		}},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("write() error = %v, want ErrInvalid", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("registry path exists after oversized write: %v", statErr)
	}
}

func TestRegistryInvalidReplacementPreservesExistingPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "write-policies.json")
	registry := NewRegistry(path)
	value := testProfile("work")
	if _, err := registry.Set(context.Background(), value, []string{"42"}); err != nil {
		t.Fatalf("initial Set() error = %v", err)
	}
	if _, err := registry.Set(context.Background(), value, []string{"042"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("replacement Set() error = %v, want ErrInvalid", err)
	}
	policy, err := registry.Get(context.Background(), value.Name)
	if err != nil || !reflect.DeepEqual(policy.Spaces, []string{"42"}) {
		t.Fatalf("policy after rejected replacement = %#v, %v", policy, err)
	}
}

func TestRegistryConcurrentSetsDoNotLosePolicies(t *testing.T) {
	registry := NewRegistry(filepath.Join(t.TempDir(), "config", "write-policies.json"))
	const count = 12
	var wait sync.WaitGroup
	errorsFound := make(chan error, count)
	for index := 0; index < count; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			name := "work" + string(rune('a'+index))
			_, err := registry.Set(context.Background(), testProfile(name), []string{"42"})
			errorsFound <- err
		}(index)
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatalf("Set() error = %v", err)
		}
	}
	for index := 0; index < count; index++ {
		name := "work" + string(rune('a'+index))
		if _, err := registry.Get(context.Background(), name); err != nil {
			t.Fatalf("Get(%q) error = %v", name, err)
		}
	}
}

func TestRegistryDoesNotWriteAfterContextCanceledWhileWaitingForMutationLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "write-policies.json")
	registry := NewRegistry(path)
	locked := make(chan struct{})
	release := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- lockfile.With(path, func() error {
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := registry.Set(ctx, testProfile("work"), []string{"42"})
		result <- err
	}()
	time.Sleep(25 * time.Millisecond)
	cancel()
	close(release)
	if err := <-holderDone; err != nil {
		t.Fatalf("lock holder error = %v", err)
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Set() error = %v, want context canceled", err)
	}
	if _, err := registry.Get(context.Background(), "work"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() error = %v, want no persisted policy", err)
	}
}

func TestWithPolicyLockHonorsContextAndSerializesCallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "write-policies.json")
	registry := NewRegistry(path)
	lockPath := path + ".profile-work"
	locked := make(chan struct{})
	release := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- lockfile.With(lockPath, func() error {
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked

	ctx, cancel := context.WithCancel(context.Background())
	called := false
	result := make(chan error, 1)
	go func() {
		result <- registry.WithPolicyLock(ctx, "work", func() error {
			called = true
			return nil
		})
	}()
	time.Sleep(25 * time.Millisecond)
	cancel()
	close(release)
	if err := <-holderDone; err != nil {
		t.Fatalf("lock holder error = %v", err)
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("WithPolicyLock() error = %v, want context canceled", err)
	}
	if called {
		t.Fatal("callback ran after context cancellation")
	}
	if err := registry.WithPolicyLock(context.Background(), "work", nil); err == nil {
		t.Fatal("nil callback was accepted")
	}
	canceled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if err := registry.WithPolicyLock(canceled, "work", func() error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled WithPolicyLock() error = %v, want context canceled", err)
	}
	broadDir := t.TempDir()
	if err := os.Chmod(broadDir, 0o755); err != nil {
		t.Fatal(err)
	}
	broadRegistry := NewRegistry(filepath.Join(broadDir, "write-policies.json"))
	broadCalled := false
	if err := broadRegistry.WithPolicyLock(context.Background(), "work", func() error {
		broadCalled = true
		return nil
	}); !errors.Is(err, ErrInsecurePermissions) {
		t.Fatalf("broad-directory WithPolicyLock() error = %v, want ErrInsecurePermissions", err)
	}
	if broadCalled {
		t.Fatal("callback ran in an insecure policy directory")
	}
}
