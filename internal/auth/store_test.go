package auth

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/abigotado/confluence-cli/internal/profile"
)

func TestCredentialValueRoundTripPreservesBinding(t *testing.T) {
	credential := Credential{
		Token: "token-sentinel", ProfileIdentity: strings.Repeat("a", 64), Generation: strings.Repeat("A", 43),
		Capabilities: []profile.Capability{profile.CapabilityRead, profile.CapabilityPageWrite},
	}
	encoded, err := encodeCredentialValue(credential)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == credential.Token || !strings.HasPrefix(string(encoded), credentialPayloadPrefix) {
		t.Fatal("bound credential was not encoded as a versioned Keychain payload")
	}
	decoded, err := decodeCredentialValue(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Token != credential.Token || decoded.Generation != credential.Generation ||
		len(decoded.Capabilities) != 2 || decoded.Capabilities[1] != profile.CapabilityPageWrite {
		t.Fatalf("decoded binding = %#v", decoded)
	}
}

func TestCredentialValueRetainsLegacyPayloadCompatibility(t *testing.T) {
	decoded, err := decodeCredentialValue([]byte("legacy-token"))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Token != "legacy-token" || decoded.Generation != "" || len(decoded.Capabilities) != 0 {
		t.Fatalf("legacy credential = %#v", decoded)
	}
}

func TestCredentialValueRejectsMalformedVersionedPayload(t *testing.T) {
	for _, suffix := range []string{
		`{"version":2,"generation":"` + strings.Repeat("A", 43) + `","capabilities":["read"],"token":"token"}`,
		`{"version":1,"generation":"` + strings.Repeat("A", 43) + `","capabilities":["read"],"token":"token","extra":true}`,
		`{"version":1,"generation":"bad","capabilities":["read"],"token":"token"}`,
	} {
		if _, err := decodeCredentialValue([]byte(credentialPayloadPrefix + suffix)); err == nil {
			t.Fatalf("malformed payload accepted: %s", suffix)
		}
	}
}

func TestValidateCredentialBindingFailsClosed(t *testing.T) {
	generation := strings.Repeat("A", 43)
	modern := profile.Profile{
		Name: "work", Site: "https://tenant.atlassian.net", Email: "user@example.com", CloudID: "cloud-id",
		CredentialGeneration: generation, Capabilities: []profile.Capability{profile.CapabilityRead, profile.CapabilityPageWrite},
	}
	matching := Credential{Token: "token", ProfileIdentity: profile.CredentialIdentity(modern), Generation: generation, Capabilities: append([]profile.Capability(nil), modern.Capabilities...)}
	if err := ValidateCredentialBinding(matching, modern); err != nil {
		t.Fatalf("matching binding rejected: %v", err)
	}
	for _, credential := range []Credential{
		{Token: "token"},
		{Token: "token", ProfileIdentity: strings.Repeat("b", 64), Generation: generation, Capabilities: append([]profile.Capability(nil), modern.Capabilities...)},
		{Token: "token", ProfileIdentity: profile.CredentialIdentity(modern), Generation: strings.Repeat("A", 42) + "Q", Capabilities: append([]profile.Capability(nil), modern.Capabilities...)},
		{Token: "token", ProfileIdentity: profile.CredentialIdentity(modern), Generation: generation, Capabilities: []profile.Capability{profile.CapabilityRead}},
	} {
		if err := ValidateCredentialBinding(credential, modern); !errors.Is(err, ErrCredentialBindingMismatch) {
			t.Fatalf("binding error = %v, want ErrCredentialBindingMismatch", err)
		}
	}
}

func TestValidateCredentialBindingRejectsExpiryOnlyMutation(t *testing.T) {
	modern := profile.Profile{
		Name: "work", Site: "https://tenant.atlassian.net", Email: "user@example.com", CloudID: "cloud-id",
		CredentialGeneration: strings.Repeat("A", 43), Capabilities: []profile.Capability{profile.CapabilityRead},
	}
	credential := Credential{
		Token: "token", ProfileIdentity: profile.CredentialIdentity(modern), Generation: modern.CredentialGeneration,
		Capabilities: append([]profile.Capability(nil), modern.Capabilities...),
	}
	expiresAt := time.Date(2035, time.June, 7, 8, 9, 10, 0, time.UTC)
	modern.ExpiresAt = &expiresAt
	if err := ValidateCredentialBinding(credential, modern); !errors.Is(err, ErrCredentialBindingMismatch) {
		t.Fatalf("binding error = %v, want ErrCredentialBindingMismatch", err)
	}
}
