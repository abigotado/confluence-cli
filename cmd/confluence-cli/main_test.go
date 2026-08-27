package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/abigotado/confluence-cli/internal/errx"
	"github.com/abigotado/confluence-cli/internal/output"
)

func TestRunWithRecoveryMapsPanicToInternalEnvelope(t *testing.T) {
	var stdout bytes.Buffer
	code := runWithRecovery(func() errx.Code {
		panic("PANIC_SENTINEL")
	}, &stdout)
	if code != errx.CodeInternal {
		t.Fatalf("code=%d want=%d", code, errx.CodeInternal)
	}
	var envelope output.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.OK || envelope.Error == nil || envelope.Error.Code != "INTERNAL" || bytes.Contains(stdout.Bytes(), []byte("PANIC_SENTINEL")) {
		t.Fatalf("panic envelope = %s", stdout.String())
	}
}
