// Package profile stores non-secret Confluence Cloud connection metadata.
//
// Profile selection is deliberately explicit for every invocation. This
// package has no active or default profile state.
package profile

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/abigotado/confluence-cli/internal/atlassian"
)

const (
	maxNameLength              = 64
	maxCloudIDLength           = atlassian.MaxCloudIDLength
	credentialGenerationBytes  = 32
	credentialGenerationLength = 43
)

// Capability identifies a locally recorded credential capability. It is
// non-secret metadata and does not replace Confluence's authorization checks.
type Capability string

const (
	// CapabilityRead is present on every modern profile.
	CapabilityRead Capability = "read"
	// CapabilityPageWrite records an explicitly selected page-write token.
	CapabilityPageWrite Capability = "page-write"
)

// CredentialIdentity returns the stable SHA-256 binding for every non-secret
// profile field that determines where and how a credential may be used.
func CredentialIdentity(value Profile) string {
	capabilities := make([]string, 0, len(value.Capabilities))
	for _, capability := range value.Capabilities {
		capabilities = append(capabilities, string(capability))
	}
	expiry := "expires_at=absent"
	if value.ExpiresAt != nil {
		expiry = "expires_at=utc:" + value.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	canonical := strings.Join([]string{
		value.Name,
		value.Site,
		strings.ToLower(value.Email),
		value.CloudID,
		value.CredentialGeneration,
		strings.Join(capabilities, ","),
		expiry,
	}, "|")
	digest := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(digest[:])
}

var (
	// ErrProfileRequired means a caller did not explicitly select a profile.
	ErrProfileRequired = errors.New("a profile is required for every invocation")
	// ErrInvalidProfile means profile metadata failed validation.
	ErrInvalidProfile = errors.New("invalid profile")
	// ErrNotFound means the requested profile is not registered.
	ErrNotFound = errors.New("profile not found")
	// ErrAlreadyExists means Add would overwrite an existing profile.
	ErrAlreadyExists = errors.New("profile already exists")
	// ErrCorruptRegistry means the registry cannot be decoded or validated.
	ErrCorruptRegistry = errors.New("profile registry is corrupt")
	// ErrInsecurePermissions means registry metadata is accessible too broadly.
	ErrInsecurePermissions = errors.New("profile registry has insecure permissions")

	namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// CommitError reports that a registry mutation reached its atomic rename but
// a subsequent durability step failed. Callers must treat the requested
// metadata state as committed and must not compensate it as a pre-commit
// failure.
type CommitError struct {
	Err error
}

func (e *CommitError) Error() string {
	return fmt.Sprintf("profile metadata committed but durability check failed: %v", e.Err)
}

func (e *CommitError) Unwrap() error {
	return e.Err
}

// WasCommitted identifies a post-rename registry failure.
func WasCommitted(err error) bool {
	var committed *CommitError
	return errors.As(err, &committed)
}

// Profile contains only non-secret Confluence Cloud connection metadata.
type Profile struct {
	Name                 string       `json:"name"`
	Site                 string       `json:"site"`
	Email                string       `json:"email"`
	CloudID              string       `json:"cloud_id"`
	ExpiresAt            *time.Time   `json:"expires_at,omitempty"`
	CredentialGeneration string       `json:"credential_generation,omitempty"`
	Capabilities         []Capability `json:"capabilities,omitempty"`

	credentialGenerationPresent bool
	capabilitiesPresent         bool
}

// UnmarshalJSON preserves whether modern metadata fields were present so null
// or partial states cannot be mistaken for a valid legacy profile.
func (p *Profile) UnmarshalJSON(raw []byte) error {
	type wireProfile struct {
		Name                 string          `json:"name"`
		Site                 string          `json:"site"`
		Email                string          `json:"email"`
		CloudID              string          `json:"cloud_id"`
		ExpiresAt            *time.Time      `json:"expires_at,omitempty"`
		CredentialGeneration json.RawMessage `json:"credential_generation"`
		Capabilities         json.RawMessage `json:"capabilities"`
	}
	var wire wireProfile
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	if err := requireDecodeEOF(decoder); err != nil {
		return err
	}
	decoded := Profile{
		Name:      wire.Name,
		Site:      wire.Site,
		Email:     wire.Email,
		CloudID:   wire.CloudID,
		ExpiresAt: wire.ExpiresAt,
	}
	if wire.CredentialGeneration != nil {
		decoded.credentialGenerationPresent = true
		if bytes.Equal(wire.CredentialGeneration, []byte("null")) {
			return fmt.Errorf("%w: credential_generation cannot be null", ErrInvalidProfile)
		}
		if err := json.Unmarshal(wire.CredentialGeneration, &decoded.CredentialGeneration); err != nil {
			return fmt.Errorf("%w: credential_generation must be a string", ErrInvalidProfile)
		}
	}
	if wire.Capabilities != nil {
		decoded.capabilitiesPresent = true
		if bytes.Equal(wire.Capabilities, []byte("null")) {
			return fmt.Errorf("%w: capabilities cannot be null", ErrInvalidProfile)
		}
		if err := json.Unmarshal(wire.Capabilities, &decoded.Capabilities); err != nil {
			return fmt.Errorf("%w: capabilities must be an array", ErrInvalidProfile)
		}
	}
	*p = decoded
	return nil
}

func requireDecodeEOF(decoder *json.Decoder) error {
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

// RequireName validates the mandatory per-invocation profile selection.
func RequireName(name string) error {
	if name == "" {
		return ErrProfileRequired
	}
	return ValidateName(name)
}

// ValidateName validates a profile name for exact use as a Keychain account.
func ValidateName(name string) error {
	if name == "" || len(name) > maxNameLength || !namePattern.MatchString(name) {
		return fmt.Errorf("%w: name must be 1-%d ASCII letters, digits, dot, dash, or underscore and start with a letter or digit", ErrInvalidProfile, maxNameLength)
	}
	return nil
}

// Validate checks all profile metadata.
func (p Profile) Validate() error {
	if err := p.validateConnection(); err != nil {
		return err
	}
	hasGeneration := p.CredentialGeneration != "" || p.credentialGenerationPresent
	hasCapabilities := p.Capabilities != nil || p.capabilitiesPresent
	if !hasGeneration && !hasCapabilities {
		return nil
	}
	if !hasGeneration || !hasCapabilities {
		return fmt.Errorf("%w: credential_generation and capabilities must either both be present or both be absent", ErrInvalidProfile)
	}
	if err := ValidateCredentialGeneration(p.CredentialGeneration); err != nil {
		return err
	}
	return ValidateCapabilities(p.Capabilities)
}

func (p Profile) validateConnection() error {
	if err := ValidateName(p.Name); err != nil {
		return err
	}
	if err := ValidateSite(p.Site); err != nil {
		return err
	}
	if err := ValidateEmail(p.Email); err != nil {
		return err
	}
	if err := ValidateCloudID(p.CloudID); err != nil {
		return err
	}
	if p.ExpiresAt != nil && p.ExpiresAt.IsZero() {
		return fmt.Errorf("%w: expires_at must be a valid timestamp", ErrInvalidProfile)
	}
	return nil
}

// ValidateLoginIntent validates connection metadata and an optional canonical
// capability selection. Callers must not supply a credential generation;
// Login creates it after accepting the intent.
func (p Profile) ValidateLoginIntent() error {
	if err := p.validateConnection(); err != nil {
		return err
	}
	if p.CredentialGeneration != "" || p.credentialGenerationPresent {
		return fmt.Errorf("%w: credential_generation is generated by login", ErrInvalidProfile)
	}
	if p.Capabilities == nil && !p.capabilitiesPresent {
		return nil
	}
	return ValidateCapabilities(p.Capabilities)
}

// WithNewCredentialGeneration returns modern profile metadata for a successful
// login intent. Empty capabilities default to the read-only capability set.
func (p Profile) WithNewCredentialGeneration() (Profile, error) {
	if err := p.ValidateLoginIntent(); err != nil {
		return Profile{}, err
	}
	if p.Capabilities == nil {
		p.Capabilities = []Capability{CapabilityRead}
	} else {
		p.Capabilities = append([]Capability(nil), p.Capabilities...)
	}
	generation, err := NewCredentialGeneration()
	if err != nil {
		return Profile{}, err
	}
	p.CredentialGeneration = generation
	p.credentialGenerationPresent = false
	p.capabilitiesPresent = false
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// NewCredentialGeneration returns 32 cryptographically random bytes encoded
// as an unpadded, fixed-length base64url identifier.
func NewCredentialGeneration() (string, error) {
	raw := make([]byte, credentialGenerationBytes)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", fmt.Errorf("generate credential generation: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// ValidateCredentialGeneration validates the canonical random identifier
// representation without treating it as secret material.
func ValidateCredentialGeneration(generation string) error {
	if len(generation) != credentialGenerationLength {
		return fmt.Errorf("%w: credential_generation must be a %d-character base64url value", ErrInvalidProfile, credentialGenerationLength)
	}
	raw, err := base64.RawURLEncoding.DecodeString(generation)
	if err != nil || len(raw) != credentialGenerationBytes || base64.RawURLEncoding.EncodeToString(raw) != generation {
		return fmt.Errorf("%w: credential_generation must encode %d bytes as unpadded base64url", ErrInvalidProfile, credentialGenerationBytes)
	}
	return nil
}

// ValidateCapabilities accepts only the two canonical, ordered capability
// sets. This avoids multiple encodings of the same policy identity.
func ValidateCapabilities(capabilities []Capability) error {
	validRead := len(capabilities) == 1 && capabilities[0] == CapabilityRead
	validPageWrite := len(capabilities) == 2 && capabilities[0] == CapabilityRead && capabilities[1] == CapabilityPageWrite
	if !validRead && !validPageWrite {
		return fmt.Errorf("%w: capabilities must be exactly [%q] or [%q,%q]", ErrInvalidProfile, CapabilityRead, CapabilityRead, CapabilityPageWrite)
	}
	return nil
}

// HasCapability reports membership in a validated canonical capability set.
func (p Profile) HasCapability(capability Capability) bool {
	for _, candidate := range p.Capabilities {
		if candidate == capability {
			return true
		}
	}
	return false
}

// ValidateSite accepts only an exact lowercase Atlassian Cloud tenant origin.
func ValidateSite(raw string) error {
	if err := atlassian.ValidateSite(raw); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidProfile, err)
	}
	return nil
}

// ValidateEmail accepts a plain address suitable for Atlassian Basic auth.
func ValidateEmail(email string) error {
	if err := atlassian.ValidateEmail(email); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidProfile, err)
	}
	return nil
}

// ValidateCloudID validates the non-secret tenant identifier used by the
// scoped-token API gateway.
func ValidateCloudID(cloudID string) error {
	if err := atlassian.ValidateCloudID(cloudID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidProfile, err)
	}
	return nil
}
