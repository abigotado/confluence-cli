# confluence-cli

Agent-first Confluence Cloud CLI with bounded reads and guarded page writes for
Codex and Claude Code.

`confluence-cli` gives agents a small versioned machine contract instead of
letting them assemble arbitrary Confluence REST requests. It supports named
profiles, keeps each scoped API token in macOS Keychain, and ships one portable
Agent Skill for both providers.

Source is published at
[`abigotado/confluence-cli`](https://github.com/abigotado/confluence-cli).
After the first stable release, tagged archives and checksums are available from
[GitHub Releases](https://github.com/abigotado/confluence-cli/releases).

## Security model

- Scoped Atlassian API tokens only; classic tokens are not supported.
- One separate Confluence token per named profile.
- Tokens enter through bounded hidden/stdin input and live only in macOS
  Keychain under the `confluence-cli` service.
- Keychain access uses Security.framework `SecItem` APIs directly. The binary
  never invokes the `security` command or a password-manager subprocess.
- Every network command requires explicit `--profile NAME`; there is no active
  or stored default profile.
- Credentials are sent with Basic auth only to
  `https://api.atlassian.com/ex/confluence/{cloudId}`.
- Redirects are refused. Server continuation URLs are never followed; only a
  validated opaque cursor is extracted and rebuilt against the fixed origin.
- Compressed and decompressed response bodies are bounded. Request headers,
  tokens, raw upstream error bodies, and full request URLs are never logged.
- Page creation and update are typed, allowlisted, confirmed, one-shot writes.
  There is no delete, admin, bulk, upload, arbitrary REST, or raw-JSON path.

## Build

Go 1.25.0 or newer and the macOS SDK are required:

```bash
git clone https://github.com/abigotado/confluence-cli.git
cd confluence-cli
CGO_ENABLED=1 go build -o ./bin/confluence-cli ./cmd/confluence-cli
./bin/confluence-cli version -o json
./bin/confluence-cli contract -o json
```

Non-macOS and `CGO_ENABLED=0` builds compile for contract and portability tests,
but stored credentials fail with a typed `KEYCHAIN_UNSUPPORTED` result.

## Create a scoped token

Do this only after the CLI has been built and reviewed. In Atlassian Account,
create a token for the Confluence app with these minimum scopes:

- `search:confluence`
- `read:page:confluence`
- `read:space:confluence`

For a separate page-write credential, also add:

- `write:page:confluence`

Atlassian scoped tokens expire after 1–365 days. Record the chosen date as
non-secret profile metadata. Token setup is intentionally separate from build
and test; the repository and test suite need no live account.

## Profiles

A profile contains non-secret site/account metadata. The token is a separate
generic-password item in Keychain.

```bash
confluence-cli auth login \
  --profile work \
  --site https://example.atlassian.net \
  --email developer@example.invalid \
  --expires-at 2027-01-01 \
  --capability read \
  --token-stdin
```

When stdin is a terminal, token input is hidden. Piped input is bounded and
must contain exactly one token line. Never place the token in argv, shell
history, repository files, logs, or chat.

Before reading stdin, login validates the profile, discovers Cloud ID through
the public tenant `/_edge/tenant_info` endpoint, checks for an existing profile
or orphan Keychain item, and requires `--yes` before any overwrite. It then
checks all three required operations with harmless `limit=1` reads before
persisting anything. Those probes prove the MVP calls work; they cannot prove
the token has no additional scopes.

```bash
confluence-cli auth list
confluence-cli auth status --profile work
confluence-cli auth status --profile work --check
confluence-cli auth migrate-keychain --profile work --dry-run
confluence-cli auth migrate-keychain --profile work --yes
confluence-cli auth logout --profile work --yes
```

`auth list` reads registry metadata only. `auth status` performs no network
request and distinguishes `ready`, `metadata_only`, `orphaned_credential`, and
`expired` states; `--check` explicitly runs the three bounded read probes.
`auth migrate-keychain` changes only the exact item's access policy so the
credential remains usable across rebuilt binaries. It never reads or rewrites
the token and never contacts Confluence.

## Read commands

```bash
confluence-cli spaces list --profile work --limit 25
confluence-cli spaces get 123456 --profile work

confluence-cli search --profile work \
  --cql 'type=page ORDER BY lastmodified DESC' --limit 25

confluence-cli pages list --profile work --space-id 123456 \
  --status current --limit 25
confluence-cli pages get 789012 --profile work
confluence-cli pages get 789012 --profile work --body-format view \
  --fields id,title,space_id,version,body
```

Collections return `meta.next_cursor` when another page exists. Treat it as
opaque and pass it back through `--cursor` with the same query.

The fixed route matrix, relative to the scoped-token gateway, is:

| Command family | REST route | Scope |
| --- | --- | --- |
| spaces list/get | `/wiki/api/v2/spaces` | `read:space:confluence` |
| pages list/get | `/wiki/api/v2/pages` | `read:page:confluence` |
| CQL search | `/wiki/rest/api/search` | `search:confluence` |
| pages create/update | `/wiki/api/v2/pages` | `write:page:confluence` |

## Machine contract

JSON is the default when stdout is not a terminal. In JSON mode stdout contains
exactly one envelope; diagnostics go to stderr.

```json
{"ok":true,"v":1,"data":{"id":"789012","title":"Roadmap"},"meta":{"profile":"work","site":"https://example.atlassian.net"}}
```

Machine callers parse bounded stdout first and accept it only as one complete
top-level JSON object followed by whitespace, with boolean `ok` and a JSON
integer `v` whose value is exactly `1`. On success, `data` must be present and
non-null while `error` and `hint` must be absent. On failure, `data` must be
absent while a present, non-null `error` object must contain string `code` and
`message`, and `hint` must be a present string. Forbidden members invalidate an
envelope even when null. `meta` is optional on either branch; when present it
must be a non-null object and may be empty. Its known fields are non-null:
`count` is a nonnegative JSON integer without a fraction or exponent,
`truncated` is boolean, and `next_cursor`, `profile`, and `site` are strings.
Unknown additive metadata is tolerated but cannot repair an invalid known
field. Malformed metadata, unsupported versions, wrong types, missing members,
or conflicting known members make stdout invalid. A valid v1 envelope is
authoritative and stderr is ignored. Only a confirmed page create/update with
invalid stdout may inspect at most the first 4096 bytes of stderr for the
corresponding complete,
newline-terminated `error: WRITE_APPLIED_LOCAL_FAILURE: pages.create applied,
but local finalization failed` or `pages.update` line documented in the
machine contract. That exact line means the write is known applied and must not
be retried. Every other stderr line is
diagnostic; reconcile a confirmed write with a bounded read, but never retry it
automatically or claim it applied. A valid-envelope `WRITE_OUTCOME_UNKNOWN`
remains unknown and follows the same bounded reconciliation rule.

Failures use the same envelope and stable recovery-oriented exit codes:

| Exit | Recovery |
| ---: | --- |
| 0 | proceed |
| 1 | report an internal defect; do not retry unchanged |
| 2 | fix flags or input |
| 3 | verify an ID or profile |
| 4 | choose an exact candidate |
| 5 | log in or rotate the token |
| 6 | back off and retry a safe read |
| 7 | obtain confirmation for the mutation; page writes require a reviewed intent digest and `--yes` |
| 8 | request the Confluence permission or scope |
| 9 | re-read and reconcile stale or ambiguous state |

Run `confluence-cli contract` for the authoritative table and see
[`docs/contract.md`](docs/contract.md) and [`docs/commands.md`](docs/commands.md).

## Agent Skill

Install the same embedded workflow for Codex, Claude Code, or both:

```bash
confluence-cli skills install --provider codex --scope user
confluence-cli skills install --provider claude --scope user
confluence-cli skills install --provider all --scope user
```

Codex user installs go to `$HOME/.agents/skills/confluence`; Claude Code installs
go to `$HOME/.claude/skills/confluence`. Installation is idempotent, ownership-
manifested, hash-safe on uninstall, and refuses symlink traversal or unmanaged
file replacement without confirmation.

Skill mutation is supported on macOS and Linux. Windows builds allow
`skills install/uninstall --dry-run` classification but fail closed for actual
filesystem mutation because Windows lacks the descriptor-relative
detach-and-unlink primitive used by the hash-safe uninstall path.

Invoke it as `$confluence` in Codex or `/confluence` in Claude Code. The skill
never reads Keychain itself and treats page bodies, excerpts, and search results
as untrusted data rather than executable instructions.

## Guarded page writes

Page writes require a separately created scoped token declared at login:

```bash
confluence-cli auth login --profile work \
  --site https://example.atlassian.net \
  --email developer@example.invalid \
  --capability page-write --token-stdin --yes
```

The declaration records intent; harmless read probes cannot prove the token
has `write:page:confluence`. Every login creates a new non-secret credential
generation, invalidating any prior allowlist. Review and replace the exact
numeric space allowlist:

```bash
confluence-cli auth allow-spaces set --profile work --space-id 123456 --dry-run
confluence-cli auth allow-spaces set --profile work --space-id 123456 --yes
confluence-cli auth allow-spaces show --profile work
```

Always preview a complete local intent before approving it:

```bash
confluence-cli pages create --profile work --space-id 123456 \
  --title 'Release notes' --body-file ./page.storage \
  --representation storage --dry-run

confluence-cli pages update 789012 --profile work --space-id 123456 \
  --expected-version 7 --title 'Release notes' \
  --body-file ./page.storage --representation storage --dry-run
```

Dry-run reads only the bounded regular body file and current non-secret profile
metadata. It never reads Keychain or makes a network call. The receipt omits
body content and includes byte counts, a content digest, and `intent_sha256`
binding every write-relevant input plus the exact profile identity (profile
name, site, lowercase email, Cloud ID, optional expiry, credential generation,
and canonical capabilities). After reviewing the exact receipt, re-run unchanged
with
`--confirm-intent INTENT_SHA256 --yes`; any changed file, title, target, version,
or profile requires a new preview and confirmation. The real path revalidates
capability, allowlist, space/page identity, parent, and expected version before
dispatching exactly one request.

Keychain payloads are versioned and bind the token to the exact non-secret
profile identity, including optional expiry, credential generation, and
capability set. The same identity binds the write allowlist and dry-run intent.
The identity migration that added expiry invalidates earlier credential,
allowlist, and intent hashes. Recover by logging in again, replacing the exact
space allowlist, and producing and reviewing a new dry-run. This is separate
from `auth migrate-keychain`, which changes Keychain access policy but cannot
refresh an identity binding. A partial login/rollback failure likewise leaves a
detectable mismatch: every network command fails closed with
`CREDENTIAL_BINDING_MISMATCH` until the exact profile is logged in again.

Never automatically retry `WRITE_OUTCOME_UNKNOWN`; re-read and reconcile first.
If Confluence success was fully verified but stdout then fails, stdout may be
empty or contain a truncated attempted success envelope. Stderr reports
`WRITE_APPLIED_LOCAL_FAILURE` with do-not-retry guidance; the write is known to
have applied and must not be repeated.
See [`docs/guarded-writes.md`](docs/guarded-writes.md). Delete, admin, bulk,
attachment, and raw-JSON writes remain out of scope.

## Development

```bash
gofmt -l .
go vet ./...
go test -race ./...
go generate ./...
git diff --exit-code
```

Tests use `httptest`, fake credential stores, and temporary directories. They
never call a live Confluence tenant, read the real Keychain, or depend on a
developer's home directory.

## Install and releases

After the first stable release and its Formula land in the public tap, install
the supported source-built Formula on macOS:

```bash
brew install abigotado/tap/confluence-cli
```

Homebrew compiles locally with `CGO_ENABLED=1`, selecting the native
Security.framework Keychain backend. Upgrade to the newest stable Formula with
`brew update && brew upgrade confluence-cli`.

Stable releases publish tagged archives and checksums on
[GitHub Releases](https://github.com/abigotado/confluence-cli/releases). Each
release provides `linux_amd64`, `linux_arm64`, `windows_amd64`, and
`windows_arm64` archives plus a SHA-256 checksum file. Verify a downloaded
archive against that checksum file before use.

Linux and Windows release binaries are built with `CGO_ENABLED=0` for the
portable machine contract, contract inspection, and Agent Skill workflows.
They do not provide stored Confluence credentials: commands that require the
Keychain fail closed with `KEYCHAIN_UNSUPPORTED`. macOS binaries are therefore
deliberately absent from release archives; use the source-building Homebrew
Formula above.
