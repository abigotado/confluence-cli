package profile

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestProfileValidate(t *testing.T) {
	tests := []struct {
		name    string
		profile Profile
		wantErr bool
	}{
		{
			name:    "scoped profile is valid",
			profile: Profile{Name: "work", Site: "https://tenant.atlassian.net", Email: "user@example.com", CloudID: "123e4567-e89b-12d3-a456-426614174000"},
		},
		{
			name:    "scoped profile rejects missing cloud id",
			profile: Profile{Name: "work", Site: "https://tenant.atlassian.net", Email: "user@example.com"},
			wantErr: true,
		},
		{
			name:    "cloud id rejects punctuation",
			profile: Profile{Name: "work", Site: "https://tenant.atlassian.net", Email: "user@example.com", CloudID: "cloud/id"},
			wantErr: true,
		},
		{
			name:    "cloud id rejects excessive length",
			profile: Profile{Name: "work", Site: "https://tenant.atlassian.net", Email: "user@example.com", CloudID: strings.Repeat("a", maxCloudIDLength+1)},
			wantErr: true,
		},
		{
			name:    "email rejects display name",
			profile: Profile{Name: "work", Site: "https://tenant.atlassian.net", Email: "User <user@example.com>", CloudID: "cloud"},
			wantErr: true,
		},
		{
			name:    "email rejects invalid domain label",
			profile: Profile{Name: "work", Site: "https://tenant.atlassian.net", Email: "user@invalid_domain.example", CloudID: "cloud"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.profile.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidProfile) {
				t.Fatalf("Validate() error = %v, want ErrInvalidProfile", err)
			}
		})
	}
}

func TestProfileModernMetadataValidation(t *testing.T) {
	generation, err := NewCredentialGeneration()
	if err != nil {
		t.Fatalf("NewCredentialGeneration() error = %v", err)
	}
	tests := []struct {
		name         string
		generation   string
		capabilities []Capability
		wantErr      bool
	}{
		{name: "legacy pair absent"},
		{name: "canonical read", generation: generation, capabilities: []Capability{CapabilityRead}},
		{name: "canonical page write", generation: generation, capabilities: []Capability{CapabilityRead, CapabilityPageWrite}},
		{name: "generation only", generation: generation, wantErr: true},
		{name: "capabilities only", capabilities: []Capability{CapabilityRead}, wantErr: true},
		{name: "empty capabilities", generation: generation, capabilities: []Capability{}, wantErr: true},
		{name: "write without read", generation: generation, capabilities: []Capability{CapabilityPageWrite}, wantErr: true},
		{name: "reversed capabilities", generation: generation, capabilities: []Capability{CapabilityPageWrite, CapabilityRead}, wantErr: true},
		{name: "duplicate capability", generation: generation, capabilities: []Capability{CapabilityRead, CapabilityRead}, wantErr: true},
		{name: "padded generation", generation: generation + "=", capabilities: []Capability{CapabilityRead}, wantErr: true},
		{name: "noncanonical trailing bits", generation: generation[:len(generation)-1] + "B", capabilities: []Capability{CapabilityRead}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Profile{
				Name: "work", Site: "https://tenant.atlassian.net", Email: "user@example.com", CloudID: "cloud",
				CredentialGeneration: tt.generation, Capabilities: tt.capabilities,
			}
			err := p.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewCredentialGenerationIsCanonicalAndFresh(t *testing.T) {
	first, err := NewCredentialGeneration()
	if err != nil {
		t.Fatalf("first NewCredentialGeneration() error = %v", err)
	}
	second, err := NewCredentialGeneration()
	if err != nil {
		t.Fatalf("second NewCredentialGeneration() error = %v", err)
	}
	if first == second {
		t.Fatal("two credential generations unexpectedly match")
	}
	if len(first) != credentialGenerationLength {
		t.Fatalf("generation length = %d, want %d", len(first), credentialGenerationLength)
	}
	raw, err := base64.RawURLEncoding.DecodeString(first)
	if err != nil || len(raw) != credentialGenerationBytes {
		t.Fatalf("generation decode bytes = %d, error = %v", len(raw), err)
	}
}

func TestProfileJSONFailsClosedOnMalformedModernState(t *testing.T) {
	generation, err := NewCredentialGeneration()
	if err != nil {
		t.Fatalf("NewCredentialGeneration() error = %v", err)
	}
	prefix := `{"name":"work","site":"https://tenant.atlassian.net","email":"user@example.com","cloud_id":"cloud"`
	tests := []struct {
		name string
		raw  string
	}{
		{name: "generation with absent capabilities", raw: prefix + `,"credential_generation":"` + generation + `"}`},
		{name: "absent generation with capabilities", raw: prefix + `,"capabilities":["read"]}`},
		{name: "null generation", raw: prefix + `,"credential_generation":null,"capabilities":["read"]}`},
		{name: "null capabilities", raw: prefix + `,"credential_generation":"` + generation + `","capabilities":null}`},
		{name: "empty capabilities", raw: prefix + `,"credential_generation":"` + generation + `","capabilities":[]}`},
		{name: "unknown capability", raw: prefix + `,"credential_generation":"` + generation + `","capabilities":["read","admin"]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p Profile
			decodeErr := json.Unmarshal([]byte(tt.raw), &p)
			if decodeErr == nil {
				decodeErr = p.Validate()
			}
			if decodeErr == nil {
				t.Fatal("malformed modern profile was accepted")
			}
		})
	}
}

func TestProfileValidateSite(t *testing.T) {
	tests := []struct {
		name  string
		site  string
		valid bool
	}{
		{name: "exact tenant URL", site: "https://example.atlassian.net", valid: true},
		{name: "http", site: "http://tenant.atlassian.net"},
		{name: "userinfo", site: "https://user@tenant.atlassian.net"},
		{name: "port", site: "https://tenant.atlassian.net:443"},
		{name: "empty port", site: "https://tenant.atlassian.net:"},
		{name: "path", site: "https://tenant.atlassian.net/rest"},
		{name: "trailing slash is a path", site: "https://tenant.atlassian.net/"},
		{name: "query", site: "https://tenant.atlassian.net?x=1"},
		{name: "empty query marker", site: "https://tenant.atlassian.net?"},
		{name: "fragment", site: "https://tenant.atlassian.net#x"},
		{name: "empty fragment marker", site: "https://tenant.atlassian.net#"},
		{name: "missing tenant", site: "https://atlassian.net"},
		{name: "nested tenant", site: "https://one.two.atlassian.net"},
		{name: "lookalike suffix", site: "https://tenant.atlassian.net.example.com"},
		{name: "localhost", site: "https://localhost"},
		{name: "IP address", site: "https://127.0.0.1"},
		{name: "uppercase host", site: "https://Tenant.atlassian.net"},
		{name: "invalid tenant label", site: "https://-tenant.atlassian.net"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Profile{Name: "work", Site: tt.site, Email: "user@example.com", CloudID: "cloud"}
			err := p.Validate()
			if (err == nil) != tt.valid {
				t.Fatalf("Validate() error = %v, valid %v", err, tt.valid)
			}
		})
	}
}

func TestRequireName(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr error
	}{
		{name: "explicit valid name", value: "work"},
		{name: "missing profile", wantErr: ErrProfileRequired},
		{name: "invalid profile", value: "work account", wantErr: ErrInvalidProfile},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RequireName(tt.value)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("RequireName() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
