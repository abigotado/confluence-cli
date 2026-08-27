//go:build !darwin || !cgo

package auth

import (
	"context"
	"errors"
	"testing"
)

func TestUnsupportedKeychainAccessOperationsReturnTypedError(t *testing.T) {
	store := KeychainStore{}
	if _, err := store.Exists(context.Background(), "work"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Exists() error = %v, want ErrUnsupported", err)
	}
	if err := store.MigrateKeychain(context.Background(), "work"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("MigrateKeychain() error = %v, want ErrUnsupported", err)
	}
}
