//go:build windows

package profile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryPersistenceFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), registryFilename)
	registry := NewRegistry(path)
	value := Profile{
		Name:    "work",
		Site:    "https://tenant.atlassian.net",
		Email:   "user@example.com",
		CloudID: "cloud-id",
	}

	if err := registry.Add(context.Background(), value); !errors.Is(err, ErrInsecurePermissions) {
		t.Fatalf("Add() error = %v, want ErrInsecurePermissions", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("registry path exists after fail-closed Add(): %v", err)
	}
}
