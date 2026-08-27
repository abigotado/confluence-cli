// Package errx defines typed errors carrying confluence-cli's stable exit contract.
//
// This package deliberately imports nothing else from this module. Candidate
// is defined here rather than borrowing a Confluence model so errx stays at the
// bottom of the dependency graph.
package errx

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Candidate identifies one possible match for an ambiguous or misspelled
// Confluence entity.
type Candidate struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// Error contains the stable fields used to build an error envelope.
type Error struct {
	// Code determines the process exit status.
	Code Code
	// Reason is the stable SCREAMING_SNAKE_CASE error.code.
	Reason string
	// Message is a safe human-readable explanation.
	Message string
	// Hint states the caller's next recovery action.
	Hint string
	// Candidates lists exact choices for an ambiguous lookup.
	Candidates []Candidate
	// DidYouMean lists near matches for a failed lookup.
	DidYouMean []Candidate
	// RetryAfter carries a server-advertised backoff.
	RetryAfter time.Duration

	wrapped error
}

func (e *Error) Error() string {
	if e.Message == "" {
		return e.Reason
	}
	return e.Message
}

// Unwrap exposes the underlying cause.
func (e *Error) Unwrap() error { return e.wrapped }

// Wrap attaches an underlying cause without changing contract fields.
func (e *Error) Wrap(err error) *Error {
	e.wrapped = err
	return e
}

// WithHint replaces the machine-oriented recovery hint.
func (e *Error) WithHint(format string, args ...any) *Error {
	e.Hint = fmt.Sprintf(format, args...)
	return e
}

// Translate maps expected standard-library errors onto the public contract.
func Translate(err error) error {
	if err == nil {
		return nil
	}
	var typed *Error
	if errors.As(err, &typed) {
		return err
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return Retryable("TIMEOUT", 0, "the command exceeded its time budget").
			WithHint("raise --timeout, then retry").Wrap(err)
	case errors.Is(err, context.Canceled):
		return Retryable("CANCELED", 0, "the command was interrupted before it finished").
			WithHint("re-run the command").Wrap(err)
	default:
		return err
	}
}

// ExitCode reports the process status for err. Untyped errors are internal
// failures: allowing a raw transport error to look retryable would leak layer
// details and could make an agent repeat a non-idempotent write.
func ExitCode(err error) Code {
	if err == nil {
		return CodeOK
	}
	var typed *Error
	if errors.As(err, &typed) {
		return typed.Code
	}
	return CodeInternal
}

// Usage reports invalid flags or input.
func Usage(format string, args ...any) *Error {
	return &Error{Code: CodeUsage, Reason: "USAGE", Message: fmt.Sprintf(format, args...), Hint: "check the flags against --help"}
}

// ProfileRequired reports that implicit profile selection was unsafe.
func ProfileRequired() *Error {
	return &Error{
		Code:    CodeUsage,
		Reason:  "PROFILE_REQUIRED",
		Message: "an explicit profile is required for every network command",
		Hint:    "re-run with --profile NAME",
	}
}

// Internal reports a defect in confluence-cli.
func Internal(format string, args ...any) *Error {
	return &Error{Code: CodeInternal, Reason: "INTERNAL", Message: fmt.Sprintf(format, args...), Hint: "this is a bug in confluence-cli; do not retry unchanged"}
}

// NotFound reports that no Confluence entity of kind matched query.
func NotFound(kind, query string, didYouMean []Candidate) *Error {
	hint := fmt.Sprintf("verify the %s key or name and --profile", kind)
	if len(didYouMean) > 0 {
		hint = "re-run with one of the values in did_you_mean"
	}
	return &Error{
		Code: CodeNotFound, Reason: "NOT_FOUND_" + upper(kind),
		Message: fmt.Sprintf("no %s matches %q", kind, query), Hint: hint,
		DidYouMean: didYouMean,
	}
}

// Ambiguous reports that query matched more than one Confluence entity.
func Ambiguous(kind, query string, candidates []Candidate) *Error {
	return &Error{
		Code: CodeAmbiguous, Reason: "AMBIGUOUS_" + upper(kind),
		Message:    fmt.Sprintf("%q matches %d %ss", query, len(candidates), kind),
		Hint:       fmt.Sprintf("re-run with --%s-id using an id from candidates", flagKind(kind)),
		Candidates: candidates,
	}
}

// Inexact refuses a partial match for a mutation.
func Inexact(kind, query string, candidates []Candidate) *Error {
	return &Error{
		Code: CodeUsage, Reason: "INEXACT_" + upper(kind),
		Message:    fmt.Sprintf("%q is not the exact name of a %s; a write will not use a partial match", query, kind),
		Hint:       fmt.Sprintf("re-run with the exact name or --%s-id from candidates", flagKind(kind)),
		Candidates: candidates,
	}
}

// Auth reports missing, rejected, or expired Confluence credentials.
func Auth(reason, format string, args ...any) *Error {
	return &Error{Code: CodeAuth, Reason: reason, Message: fmt.Sprintf(format, args...), Hint: "run 'confluence-cli auth login --profile NAME' or rotate the API token"}
}

// Retryable reports a rate limit or transient transport failure.
func Retryable(reason string, retryAfter time.Duration, format string, args ...any) *Error {
	hint := "back off and retry if the operation is safe to repeat"
	if retryAfter > 0 {
		hint = fmt.Sprintf("retry after %s if the operation is safe to repeat", retryAfter)
	}
	return &Error{Code: CodeRetryable, Reason: reason, Message: fmt.Sprintf(format, args...), Hint: hint, RetryAfter: retryAfter}
}

// ConfirmRequired reports an unconfirmed write.
func ConfirmRequired(action string) *Error {
	return &Error{Code: CodeConfirm, Reason: "CONFIRMATION_REQUIRED", Message: fmt.Sprintf("%s was not confirmed", action), Hint: "obtain approval, then re-run with --yes"}
}

// IntentConfirmationRequired reports that --yes was not bound to the exact
// reviewed dry-run receipt.
func IntentConfirmationRequired() *Error {
	return &Error{
		Code:    CodeConfirm,
		Reason:  "WRITE_INTENT_CONFIRMATION_REQUIRED",
		Message: "the confirmed intent does not match this page mutation",
		Hint:    "run the exact command with --dry-run, review intent_sha256, then pass it unchanged with --confirm-intent and --yes",
	}
}

// Permission reports a Confluence permission or API-token scope denial.
func Permission(reason, format string, args ...any) *Error {
	if reason == "" {
		reason = "PERMISSION_DENIED"
	}
	return &Error{Code: CodePermission, Reason: reason, Message: fmt.Sprintf(format, args...), Hint: "request the Confluence permission or API token scope; do not retry unchanged"}
}

// Conflict reports a stale-state or Confluence write conflict.
func Conflict(reason, format string, args ...any) *Error {
	if reason == "" {
		reason = "CONFLICT"
	}
	return &Error{Code: CodeConflict, Reason: reason, Message: fmt.Sprintf(format, args...), Hint: "re-read the Confluence resource before deciding whether to retry"}
}

// WriteOutcomeUnknown reports that a write was dispatched but its result
// could not be established safely. The caller must reconcile instead of
// automatically repeating the request.
func WriteOutcomeUnknown(operation string) *Error {
	action := strings.TrimSpace(operation)
	if action == "" {
		action = "write"
	}
	return &Error{
		Code:    CodeConflict,
		Reason:  "WRITE_OUTCOME_UNKNOWN",
		Message: fmt.Sprintf("the outcome of %s could not be established", action),
		Hint:    fmt.Sprintf("re-read Confluence to reconcile %s before deciding whether to retry", action),
	}
}

// WriteAppliedLocalFailure reports a verified remote write followed by a local
// finalization failure. The remote mutation is known to have applied, so the
// caller must report the local defect and must not repeat the write.
func WriteAppliedLocalFailure(operation string) *Error {
	action := strings.TrimSpace(operation)
	if action == "" {
		action = "write"
	}
	return &Error{
		Code:    CodeInternal,
		Reason:  "WRITE_APPLIED_LOCAL_FAILURE",
		Message: fmt.Sprintf("%s applied, but local finalization failed", action),
		Hint:    "the Confluence write applied; do not retry it; report the local failure",
	}
}

// WritePolicyOutcomeUnknown reports a local allowlist mutation that crossed
// its atomic commit point but could not prove post-rename durability.
func WritePolicyOutcomeUnknown(profileName string) *Error {
	name := strings.TrimSpace(profileName)
	return &Error{
		Code:    CodeConflict,
		Reason:  "WRITE_POLICY_OUTCOME_UNKNOWN",
		Message: fmt.Sprintf("the local write policy outcome for profile %q is uncertain", name),
		Hint:    "run 'confluence-cli auth allow-spaces show --profile NAME' before changing the policy again",
	}
}

// PayloadTooLarge reports a Confluence endpoint's bounded payload rejection.
func PayloadTooLarge(operation string) *Error {
	action := strings.TrimSpace(operation)
	if action == "" {
		action = "request"
	}
	return &Error{
		Code:    CodeUsage,
		Reason:  "PAYLOAD_TOO_LARGE",
		Message: fmt.Sprintf("Confluence rejected the %s payload as too large", action),
		Hint:    "shorten the plain-text fields and retry",
	}
}

func upper(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
			b.WriteByte(c - ('a' - 'A'))
		case c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			b.WriteByte(c)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func flagKind(kind string) string {
	return strings.ReplaceAll(strings.ToLower(kind), "_", "-")
}
