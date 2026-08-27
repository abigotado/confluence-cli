# Guarded create/update page contract

The CLI implements two page mutations and no generic write escape hatch:

```text
confluence-cli pages create --profile NAME --space-id ID --title TEXT \
  --body-file PATH --representation storage --dry-run

confluence-cli pages update PAGE_ID --profile NAME --space-id ID \
  --expected-version N --title TEXT --body-file PATH \
  --representation storage --dry-run
```

Delete, admin, permissions, restrictions, bulk operations, attachments,
templates, arbitrary endpoints, raw request bodies, and raw JSON are excluded.
The implementation uses the official v2 `POST /wiki/api/v2/pages` and
`PUT /wiki/api/v2/pages/{id}` routes, each requiring
`write:page:confluence`.

## Credential capability and generation

Read credentials use the minimum scopes `search:confluence`,
`read:page:confluence`, and `read:space:confluence`. Page writes require a
separately created token that also has `write:page:confluence` and an explicit
login declaration:

```text
confluence-cli auth login ... --capability page-write --token-stdin
```

The declaration is non-secret metadata; read probes cannot prove the token has
write scope. Every successful login records a fresh random
`credential_generation`. A modern profile has exactly `read` or
`read,page-write` capabilities. Legacy profiles remain usable for reads and
logout but fail closed for writes until they log in again.

## Identity-bound space allowlist

The write policy stores exact positive numeric space IDs in a separate locked,
atomic, mode-0600 registry. Its SHA-256 identity binding covers canonical
non-secret profile name, site, lowercase email, cloud ID, optional expiry,
credential generation, and capabilities. A token replacement, expiry change,
or capability change makes the prior allowlist stale.

```text
confluence-cli auth allow-spaces show --profile NAME
confluence-cli auth allow-spaces set --profile NAME --space-id ID --dry-run
confluence-cli auth allow-spaces set --profile NAME --space-id ID --yes
confluence-cli auth allow-spaces clear --profile NAME --yes
```

`set` replaces the complete list. Policy operations acquire the profile lock
before the policy lock. Rendering happens only after both locks are released.

## Local-only dry run

`--dry-run` validates a complete typed intent and reads only the named bounded
regular body file and the current non-secret profile metadata. It does not read
the write-policy registry or Keychain and does not contact Atlassian. Update requires an explicit positive
`--expected-version`; IDs are canonical positive decimals and the only supported
body representation is `storage`.

The receipt omits title and body content. It includes action, profile, space and
page IDs, parent when supplied, expected version, title/body byte counts,
representation, a local SHA-256 body digest, and `intent_sha256` over every
write-relevant input including the title, content digest, and current non-secret
profile identity. The identity binds site, email, cloud ID, credential
generation, optional expiry, and capabilities without exposing those fields in
the receipt. It reports `remote_checks: not_performed`, `applied: false`, and
`dry_run: true`.

The credential-identity migration that added optional expiry changes existing
identity hashes. A pre-migration Keychain binding, space allowlist, or dry-run
intent is not reusable. Recover by logging in again with the exact profile,
replacing the complete space allowlist, and producing and reviewing a new
dry-run before any confirmed write. `auth migrate-keychain` changes only
Keychain access policy and does not refresh these identity hashes.

Dry-run cannot claim that a remote target exists, that the expected version is
current, or that the account can write. It reads profile metadata only; it never
reads Keychain or contacts Atlassian. A caller must review this exact receipt
and separately confirm before re-running the unchanged intent with
`--confirm-intent INTENT_SHA256 --yes`. A mismatch exits 7 under the local
profile/write-policy locks, before Keychain or network access, and requires a
new dry-run and approval.

## Confirmed preflight and one-shot write

Under profile-then-policy locks, the confirmed command:

1. checks modern `page-write` capability and the generation-bound space;
2. recomputes the approved intent against the exact locked profile identity;
3. loads the exact Keychain credential and verifies its full profile-identity,
   optional-expiry, generation, and capability binding;
4. reads and verifies the canonical space;
5. for create with a parent, verifies a current parent in the same space;
6. for update, verifies a current page in the same space and exact expected
   version while preserving its parent;
7. dispatches one typed non-replayable request.

Root page creation explicitly sends `root-level=true`; other creates send the
exact parent. Create always uses `status=current`. Update sends
`expected-version + 1` and omits space/parent fields so it cannot move a page.
No write follows redirects or retries transport, 429, or 5xx responses.

Success requires exact HTTP 200 and a complete identity match. Create verifies
numeric returned ID, space, title, current status, parent/root placement, and a
positive version. Update verifies the same page/space/title/current status,
unchanged parent, and exactly the next version.

## Outcome handling

Documented rejections are typed: invalid create input, authentication,
permission/scope, changed target, conflict, oversized payload, and rate limit.
For update, a 400 after successful preflight is a stale-version conflict.

After dispatch, a transport error or timeout, redirect, 5xx, unexpected 2xx,
unreadable/oversized/invalid success body, or identity mismatch returns exit 9
with `WRITE_OUTCOME_UNKNOWN`. Never repeat that mutation automatically. Re-read
the page and reconcile ID, space, title, parent, version, and content before
deciding with the operator whether any further action is safe.

If the remote success was fully verified but releasing a local lock fails, the
CLI reports `WRITE_APPLIED_LOCAL_FAILURE` on exit 1: the write is known to have
applied and must not be repeated.

The same rule applies when stdout fails after remote success was fully verified.
The attempted success envelope may be absent or truncated on stdout, so it is
not a reliable recovery channel. Stderr reports
`WRITE_APPLIED_LOCAL_FAILURE` with explicit do-not-retry guidance. Treat the
write as applied and reconcile remotely instead of repeating it.
