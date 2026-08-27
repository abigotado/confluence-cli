//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package auth

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"
)

func TestReadTokenFromPTYIsBoundedAndRestoresTerminal(t *testing.T) {
	master, terminal, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer terminal.Close()

	fd := int(terminal.Fd())
	before, err := term.GetState(fd)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, readErr := ReadToken(terminal)
		result <- readErr
	}()
	rawDeadline := time.Now().Add(time.Second)
	for {
		current, stateErr := term.GetState(fd)
		if stateErr != nil {
			t.Fatal(stateErr)
		}
		if !reflect.DeepEqual(before, current) {
			break
		}
		if time.Now().After(rawDeadline) {
			t.Fatal("terminal echo was not disabled before reading")
		}
		time.Sleep(time.Millisecond)
	}
	writeDone := make(chan error, 1)
	go func() {
		_, writeErr := master.Write([]byte(strings.Repeat("x", MaxTokenBytes+1)))
		writeDone <- writeErr
	}()

	select {
	case readErr := <-result:
		if !errors.Is(readErr, ErrInvalidToken) {
			t.Fatalf("ReadToken() error=%v", readErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("oversized PTY token input was not bounded")
	}
	select {
	case writeErr := <-writeDone:
		if writeErr != nil {
			t.Fatalf("write PTY: %v", writeErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PTY writer did not complete")
	}
	after, err := term.GetState(fd)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("terminal state was not restored after oversized input")
	}
}
