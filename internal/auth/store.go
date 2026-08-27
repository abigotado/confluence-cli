// Package auth manages Confluence API tokens without exposing them to configuration,
// logs, or process output.
package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/abigotado/confluence-cli/internal/profile"
)

const (
	// KeychainService is the generic-password service used by confluence-cli.
	KeychainService = "confluence-cli"
	// MaxTokenBytes bounds interactive and piped token input.
	MaxTokenBytes            = 8192
	maxStoredCredentialBytes = MaxTokenBytes + 1024
	credentialPayloadPrefix  = "confluence-cli:credential:v1\x00"
)

var (
	// ErrNotFound means no token exists for the exact profile account.
	ErrNotFound = errors.New("credential not found")
	// ErrUnsupported means this build has no supported native credential store.
	ErrUnsupported = errors.New("native credential store is unsupported on this platform")
	// ErrInteractionNotAllowed means the Keychain operation would require UI.
	ErrInteractionNotAllowed = errors.New("credential store requires user interaction")
	// ErrKeychainMigrationRequired means an existing item's ACL is not stable across CLI rebuilds.
	ErrKeychainMigrationRequired = errors.New("keychain access policy requires migration")
	// ErrInvalidToken means credential input is empty or malformed.
	ErrInvalidToken = errors.New("invalid token")
	// ErrOverwriteConfirmationRequired prevents an implicit credential overwrite.
	ErrOverwriteConfirmationRequired = errors.New("credential overwrite requires confirmation")
	// ErrKeychainEntryNotFound means no exact item exists to migrate.
	ErrKeychainEntryNotFound = errors.New("keychain entry not found")
	// ErrKeychainMigrationBlocked means changing the access policy requires an interactive session.
	ErrKeychainMigrationBlocked = errors.New("keychain migration requires an interactive session")
	// ErrKeychainMigrationCanceled means the user canceled the explicit access-policy change.
	ErrKeychainMigrationCanceled = errors.New("keychain migration was canceled")
	// ErrKeychainMigrationUnavailable means this store cannot migrate access policies safely.
	ErrKeychainMigrationUnavailable = errors.New("keychain migration is unavailable")
	// ErrCredentialBindingMismatch means Keychain data does not belong to the
	// exact current profile generation and capability set.
	ErrCredentialBindingMismatch = errors.New("credential binding does not match profile")
)

// Credential contains one Confluence API token.
//
// It intentionally implements neither String nor Format: callers must never
// render this value into diagnostics or machine output.
type Credential struct {
	Token           string               `json:"-"`
	ProfileIdentity string               `json:"-"`
	Generation      string               `json:"-"`
	Capabilities    []profile.Capability `json:"-"`
}

// Format ensures accidental fmt formatting never renders the token.
func (Credential) Format(state fmt.State, _ rune) {
	// fmt.Formatter cannot return a write error to its caller.
	_, _ = io.WriteString(state, "<redacted>")
}

// Validate checks a credential without including its value in any error.
func (c Credential) Validate() error {
	if err := ValidateToken(c.Token); err != nil {
		return err
	}
	if c.ProfileIdentity == "" && c.Generation == "" && len(c.Capabilities) == 0 {
		return nil
	}
	if !validProfileIdentity(c.ProfileIdentity) {
		return fmt.Errorf("invalid credential profile identity binding")
	}
	if err := profile.ValidateCredentialGeneration(c.Generation); err != nil {
		return fmt.Errorf("invalid credential generation binding: %w", err)
	}
	if !validCapabilities(c.Capabilities) {
		return fmt.Errorf("invalid credential capability binding")
	}
	return nil
}

// ValidateCredentialBinding requires the Keychain payload to belong to the
// exact current profile. Legacy profiles accept only legacy unbound payloads.
func ValidateCredentialBinding(c Credential, p profile.Profile) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if p.CredentialGeneration == "" {
		if c.ProfileIdentity == "" && c.Generation == "" && len(c.Capabilities) == 0 {
			return nil
		}
		return ErrCredentialBindingMismatch
	}
	if c.ProfileIdentity != profile.CredentialIdentity(p) || c.Generation != p.CredentialGeneration || !slices.Equal(c.Capabilities, p.Capabilities) {
		return ErrCredentialBindingMismatch
	}
	return nil
}

type credentialPayload struct {
	Version         int                  `json:"version"`
	ProfileIdentity string               `json:"profile_identity"`
	Generation      string               `json:"generation"`
	Capabilities    []profile.Capability `json:"capabilities"`
	Token           string               `json:"token"`
}

func encodeCredentialValue(c Credential) ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if c.ProfileIdentity == "" {
		return []byte(c.Token), nil
	}
	payload, err := json.Marshal(credentialPayload{Version: 1, ProfileIdentity: c.ProfileIdentity, Generation: c.Generation, Capabilities: c.Capabilities, Token: c.Token})
	if err != nil {
		return nil, fmt.Errorf("encode credential payload: %w", err)
	}
	value := append([]byte(credentialPayloadPrefix), payload...)
	if len(value) > maxStoredCredentialBytes {
		return nil, fmt.Errorf("stored credential payload exceeds its bound")
	}
	return value, nil
}

func decodeCredentialValue(value []byte) (Credential, error) {
	if len(value) == 0 || len(value) > maxStoredCredentialBytes {
		return Credential{}, fmt.Errorf("stored credential is invalid: %w", ErrInvalidToken)
	}
	if !bytes.HasPrefix(value, []byte(credentialPayloadPrefix)) {
		legacy := Credential{Token: string(value)}
		if err := legacy.Validate(); err != nil {
			return Credential{}, fmt.Errorf("stored credential is invalid: %w", err)
		}
		return legacy, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(value[len(credentialPayloadPrefix):]))
	decoder.DisallowUnknownFields()
	var payload credentialPayload
	if err := decoder.Decode(&payload); err != nil {
		return Credential{}, fmt.Errorf("stored credential payload is invalid")
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Credential{}, fmt.Errorf("stored credential payload has trailing data")
	}
	if payload.Version != 1 {
		return Credential{}, fmt.Errorf("stored credential payload version is unsupported")
	}
	credential := Credential{Token: payload.Token, ProfileIdentity: payload.ProfileIdentity, Generation: payload.Generation, Capabilities: append([]profile.Capability(nil), payload.Capabilities...)}
	if err := credential.Validate(); err != nil {
		return Credential{}, fmt.Errorf("stored credential is invalid: %w", err)
	}
	return credential, nil
}

func validCapabilities(capabilities []profile.Capability) bool {
	return slices.Equal(capabilities, []profile.Capability{profile.CapabilityRead}) ||
		slices.Equal(capabilities, []profile.Capability{profile.CapabilityRead, profile.CapabilityPageWrite})
}

func validProfileIdentity(identity string) bool {
	if len(identity) != 64 || identity != strings.ToLower(identity) {
		return false
	}
	decoded, err := hex.DecodeString(identity)
	return err == nil && len(decoded) == sha256.Size
}

// CredentialStore persists one token under an exact profile account.
type CredentialStore interface {
	Exists(ctx context.Context, profileName string) (bool, error)
	Load(ctx context.Context, profileName string) (Credential, error)
	Save(ctx context.Context, profileName string, credential Credential) error
	Delete(ctx context.Context, profileName string) error
}

// CredentialAccessStore is the production credential boundary used by login
// and explicit Keychain migration. Its migration operation changes only the
// exact item's access policy and never returns credential material.
type CredentialAccessStore interface {
	CredentialStore
	MigrateKeychain(ctx context.Context, profileName string) error
}

// StatusError wraps a platform status code without credential material.
type StatusError struct {
	Operation string
	Status    int64
}

// Error returns a safe diagnostic containing only operation and status code.
func (e *StatusError) Error() string {
	return fmt.Sprintf("keychain %s failed with OSStatus %d", e.Operation, e.Status)
}

// ValidateToken rejects unsafe or ambiguous token input.
func ValidateToken(token string) error {
	if token == "" {
		return fmt.Errorf("%w: token is empty", ErrInvalidToken)
	}
	if len(token) > MaxTokenBytes {
		return fmt.Errorf("%w: token exceeds %d bytes", ErrInvalidToken, MaxTokenBytes)
	}
	for _, ch := range token {
		if ch == '\x00' || ch == '\r' || ch == '\n' {
			return fmt.Errorf("%w: token contains a prohibited control character", ErrInvalidToken)
		}
	}
	return nil
}
