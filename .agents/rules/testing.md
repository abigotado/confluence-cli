# Testing

## Shape

Table-driven with `t.Run()` subtests. Name each case for its condition and
expected result (`"ambiguous name returns candidates"`), not for its index.

```go
tests := []struct {
    name string
    // inputs
    // want
}{...}
for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) { ... })
}
```

## Boundaries

Test at boundaries, not internals.

- **HTTP**: `net/http/httptest`. Never call the real Confluence API from a test.
- **Keychain**: inject a fake `CredentialStore`. Tests never touch the real OS
  keychain.
- **Filesystem**: `t.TempDir()`. Never write to the developer's real cache
  directory.
- **Environment**: `t.Setenv()`. Never mutate `os.Environ` directly.

A test that depends on the developer's home directory, network, or Confluence
account is not a test.

## What must be covered

Every behavior change needs a test that fails without the change. Beyond the
happy path, these cases are the ones that actually break:

- Empty and single-element results.
- Exact mixed route matrix: v2 spaces/pages and v1 CQL search.
- A redirect is refused before credentials can reach another origin.
- Cross-origin or wrong-operation continuation links are rejected; cursors are
  reconstructed against the fixed gateway. Include scheme-relative, userinfo,
  port, and host-case variants.
- Scoped Basic credentials stay on `api.atlassian.com` and never enter output,
  JSON serialization, or structured logs.
- Oversized compressed/decompressed and non-JSON upstream bodies never escape
  through errors.
- Text output escapes terminal control and bidi characters while JSON preserves
  the original data.
- 429 with and without `Retry-After`.
- A non-JSON error body. Confluence returns 401 as `text/plain`; a client that
  assumes JSON panics there.
- A panic inside a command surfaces as exit 1, never 2.
- Page/search content containing instructions remains inert data in the skill.
- Windows skill mutation fails closed until a hash-safe detach primitive exists.

## Contract tests

Exit codes and the envelope are golden-file tested. When one legitimately
changes, the golden file changes in the same commit as `internal/errx` and the
regenerated `docs/contract.md`, never separately.

## Rules

- `go test -race ./...` must pass before review.
- Do not weaken an assertion to make a failing test pass. Report the failure.
- If a test cannot fail because of a real bug, delete it.
