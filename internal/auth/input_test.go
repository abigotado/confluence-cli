package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/term"
)

type fakeTerminal struct {
	makeRawCalls int
	restoreCalls int
}

func (terminal *fakeTerminal) MakeRaw(int) (*term.State, error) {
	terminal.makeRawCalls++
	return &term.State{}, nil
}

func (terminal *fakeTerminal) Restore(int, *term.State) error {
	terminal.restoreCalls++
	return nil
}

func TestReadToken(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantToken string
		wantErr   error
	}{
		{name: "token without newline", input: "sentinel-token", wantToken: "sentinel-token"},
		{name: "single line ending", input: "sentinel-token\n", wantToken: "sentinel-token"},
		{name: "CRLF line ending", input: "sentinel-token\r\n", wantToken: "sentinel-token"},
		{name: "empty", wantErr: ErrInvalidToken},
		{name: "only newline", input: "\n", wantErr: ErrInvalidToken},
		{name: "multiline", input: "first\nsecond\n", wantErr: ErrInvalidToken},
		{name: "NUL", input: "before\x00after", wantErr: ErrInvalidToken},
		{name: "too long", input: strings.Repeat("a", MaxTokenBytes+1), wantErr: ErrInvalidToken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ReadToken(strings.NewReader(tt.input))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ReadToken() error = %v, want %v", err, tt.wantErr)
			}
			if got.Token != tt.wantToken {
				t.Fatalf("ReadToken().Token = %q, want %q", got.Token, tt.wantToken)
			}
		})
	}
}

func TestBoundedTerminalReadRestoresStateAndRejectsOversize(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantBytes int
		wantErr   error
	}{
		{name: "exact limit", input: strings.Repeat("a", MaxTokenBytes) + "\r", wantBytes: MaxTokenBytes},
		{name: "oversized", input: strings.Repeat("a", MaxTokenBytes+1), wantErr: ErrInvalidToken},
		{name: "backspace edits without echo", input: "abc\x7fd\r", wantBytes: 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			terminal := &fakeTerminal{}
			raw, err := readBoundedTerminal(strings.NewReader(test.input), 7, terminal)
			if !errors.Is(err, test.wantErr) || len(raw) != test.wantBytes {
				t.Fatalf("bytes=%d err=%v, want bytes=%d err=%v", len(raw), err, test.wantBytes, test.wantErr)
			}
			if terminal.makeRawCalls != 1 || terminal.restoreCalls != 1 {
				t.Fatalf("terminal calls makeRaw=%d restore=%d", terminal.makeRawCalls, terminal.restoreCalls)
			}
		})
	}
}

func TestCredentialFormattingAndJSONAreRedacted(t *testing.T) {
	credential := Credential{Token: "non-secret-sentinel"}
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		if got := fmt.Sprintf(format, credential); strings.Contains(got, credential.Token) {
			t.Fatalf("format %q exposed the credential", format)
		}
	}
	raw, err := json.Marshal(credential)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if strings.Contains(string(raw), credential.Token) {
		t.Fatal("JSON exposed the credential")
	}
}
