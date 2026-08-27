//go:build darwin && cgo

package auth

import (
	"errors"
	"testing"
)

func TestNormalizeAllowAnyACLRejectsAmbiguousDecryptACLs(t *testing.T) {
	for _, count := range []int{0, 2} {
		flags, changed, err := normalizeAllowAnyACL(count, true, keychainPromptRequirePass)
		var countErr *decryptACLCountError
		if !errors.As(err, &countErr) || countErr.count != count {
			t.Fatalf("count %d error = %v, want decryptACLCountError", count, err)
		}
		if flags != 0 || changed {
			t.Fatalf("count %d result = flags:%d changed:%t", count, flags, changed)
		}
	}
}

func TestNormalizeAllowAnyACLPolicy(t *testing.T) {
	const otherPromptFlag uint16 = 1 << 15
	tests := []struct {
		name                 string
		applicationListIsNil bool
		promptFlags          uint16
		wantFlags            uint16
		wantChanged          bool
	}{
		{name: "already compatible", applicationListIsNil: true, promptFlags: otherPromptFlag, wantFlags: otherPromptFlag},
		{name: "application allowlist is removed", promptFlags: otherPromptFlag, wantFlags: otherPromptFlag, wantChanged: true},
		{name: "passphrase requirement is removed", applicationListIsNil: true, promptFlags: otherPromptFlag | keychainPromptRequirePass, wantFlags: otherPromptFlag, wantChanged: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags, changed, err := normalizeAllowAnyACL(1, tt.applicationListIsNil, tt.promptFlags)
			if err != nil {
				t.Fatalf("normalizeAllowAnyACL() error = %v", err)
			}
			if flags != tt.wantFlags || changed != tt.wantChanged {
				t.Fatalf("result = flags:%#x changed:%t, want flags:%#x changed:%t", flags, changed, tt.wantFlags, tt.wantChanged)
			}
		})
	}
}

func TestTranslateKeychainStatus(t *testing.T) {
	const genericFailure = -50
	tests := []struct {
		status  int64
		wantErr error
	}{
		{status: keychainStatusSuccess},
		{status: keychainStatusItemNotFound, wantErr: ErrNotFound},
		{status: keychainStatusNoInteraction, wantErr: ErrInteractionNotAllowed},
		{status: keychainStatusUserCanceled, wantErr: ErrKeychainMigrationCanceled},
		{status: genericFailure},
	}
	for _, tt := range tests {
		err := translateStatusCode("test", tt.status)
		if tt.status == keychainStatusSuccess {
			if err != nil {
				t.Fatalf("success error = %v", err)
			}
			continue
		}
		if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
			t.Fatalf("status %d error = %v, want %v", tt.status, err, tt.wantErr)
		}
		if tt.status == genericFailure {
			var statusErr *StatusError
			if !errors.As(err, &statusErr) || statusErr.Operation != "test" || statusErr.Status != genericFailure {
				t.Fatalf("generic error = %#v", err)
			}
		}
	}
}

func TestRunCompatibleKeychainOperationRequiresExplicitMigration(t *testing.T) {
	operationCalled := false
	err := runCompatibleKeychainOperation(
		func() (bool, error) { return false, nil },
		func() error {
			operationCalled = true
			return nil
		},
	)
	if !errors.Is(err, ErrKeychainMigrationRequired) {
		t.Fatalf("error = %v, want ErrKeychainMigrationRequired", err)
	}
	if operationCalled {
		t.Fatal("ordinary Keychain operation ran before migration")
	}
}

func TestRunCompatibleKeychainOperationPreservesInspectionError(t *testing.T) {
	operationCalled := false
	err := runCompatibleKeychainOperation(
		func() (bool, error) { return false, ErrInteractionNotAllowed },
		func() error {
			operationCalled = true
			return nil
		},
	)
	if !errors.Is(err, ErrInteractionNotAllowed) || errors.Is(err, ErrKeychainMigrationRequired) {
		t.Fatalf("error = %v, want original inspection error", err)
	}
	if operationCalled {
		t.Fatal("operation ran after inspection error")
	}
}

func TestRunCompatibleKeychainOperationRunsOnce(t *testing.T) {
	wantErr := errors.New("operation failed")
	calls := 0
	err := runCompatibleKeychainOperation(
		func() (bool, error) { return true, nil },
		func() error {
			calls++
			return wantErr
		},
	)
	if !errors.Is(err, wantErr) || calls != 1 {
		t.Fatalf("error = %v, calls = %d", err, calls)
	}
}
