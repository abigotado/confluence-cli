//go:build windows

package writepolicy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryPersistenceFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "write-policies.json")
	registry := NewRegistry(path)

	if _, err := registry.Set(context.Background(), testProfile("work"), []string{"42"}); !errors.Is(err, ErrInsecurePermissions) {
		t.Fatalf("Set() error = %v, want ErrInsecurePermissions", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("registry path exists after fail-closed Set(): %v", err)
	}
}
