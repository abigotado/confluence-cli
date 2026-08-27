//go:build !darwin || !cgo

package auth

import "context"

// KeychainStore is unavailable outside cgo-enabled macOS builds.
type KeychainStore struct{}

var _ CredentialAccessStore = KeychainStore{}

// Exists returns ErrUnsupported on unsupported builds.
func (KeychainStore) Exists(ctx context.Context, _ string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return false, ErrUnsupported
}

// Load returns ErrUnsupported on unsupported builds.
func (KeychainStore) Load(ctx context.Context, _ string) (Credential, error) {
	if err := ctx.Err(); err != nil {
		return Credential{}, err
	}
	return Credential{}, ErrUnsupported
}

// Save returns ErrUnsupported on unsupported builds.
func (KeychainStore) Save(ctx context.Context, _ string, _ Credential) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrUnsupported
}

// Delete returns ErrUnsupported on unsupported builds.
func (KeychainStore) Delete(ctx context.Context, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrUnsupported
}

// MigrateKeychain returns ErrUnsupported on unsupported builds.
func (KeychainStore) MigrateKeychain(ctx context.Context, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrUnsupported
}
