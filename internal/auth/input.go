package auth

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/term"
)

type fileDescriptor interface {
	Fd() uintptr
}

type terminalController interface {
	MakeRaw(fd int) (*term.State, error)
	Restore(fd int, state *term.State) error
}

type systemTerminal struct{}

func (systemTerminal) MakeRaw(fd int) (*term.State, error) { return term.MakeRaw(fd) }
func (systemTerminal) Restore(fd int, state *term.State) error {
	return term.Restore(fd, state)
}

// ReadToken reads one bounded token. Terminal input is read without echo.
func ReadToken(input io.Reader) (Credential, error) {
	if input == nil {
		return Credential{}, fmt.Errorf("%w: no input", ErrInvalidToken)
	}
	var raw []byte
	var err error
	if source, ok := input.(fileDescriptor); ok && term.IsTerminal(int(source.Fd())) {
		raw, err = readBoundedTerminal(input, int(source.Fd()), systemTerminal{})
	} else {
		raw, err = io.ReadAll(io.LimitReader(input, MaxTokenBytes+2))
	}
	if err != nil {
		return Credential{}, fmt.Errorf("read token: %w", err)
	}
	if len(raw) > MaxTokenBytes+1 {
		return Credential{}, fmt.Errorf("%w: token exceeds %d bytes", ErrInvalidToken, MaxTokenBytes)
	}
	token := string(raw)
	if strings.HasSuffix(token, "\r\n") {
		token = strings.TrimSuffix(token, "\r\n")
	} else {
		token = strings.TrimSuffix(token, "\n")
	}
	credential := Credential{Token: token}
	if err := credential.Validate(); err != nil {
		return Credential{}, err
	}
	return credential, nil
}

func readBoundedTerminal(input io.Reader, fd int, terminal terminalController) (raw []byte, err error) {
	state, err := terminal.MakeRaw(fd)
	if err != nil {
		return nil, fmt.Errorf("disable terminal echo: %w", err)
	}
	defer func() {
		if restoreErr := terminal.Restore(fd, state); restoreErr != nil && err == nil {
			raw = nil
			err = fmt.Errorf("restore terminal: %w", restoreErr)
		}
	}()

	raw = make([]byte, 0, MaxTokenBytes)
	one := []byte{0}
	for {
		_, readErr := io.ReadFull(input, one)
		if readErr != nil {
			if len(raw) > 0 && (errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF)) {
				return raw, nil
			}
			return nil, readErr
		}
		switch one[0] {
		case '\r', '\n':
			return raw, nil
		case '\b', 0x7f:
			if len(raw) > 0 {
				raw = raw[:len(raw)-1]
			}
		case 0x03:
			return nil, errors.New("token input interrupted")
		default:
			if len(raw) == MaxTokenBytes {
				return nil, fmt.Errorf("%w: token exceeds %d bytes", ErrInvalidToken, MaxTokenBytes)
			}
			raw = append(raw, one[0])
		}
	}
}
