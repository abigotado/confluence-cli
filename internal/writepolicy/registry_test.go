package writepolicy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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
	if got, want := IdentityFor(value), "7b315e08e9f8f6ffa18ffd9bacb4219001855a1bc2da7a6490cefa06dae77f07"; got != want {
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
		{name: "expiry", change: func(p profile.Profile) profile.Profile {
			expiresAt := time.Date(2035, time.June, 7, 8, 9, 10, 123456789, time.UTC)
			p.ExpiresAt = &expiresAt
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
