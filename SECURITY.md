# Security policy

`confluence-cli` stores Confluence Cloud API tokens in macOS Keychain and uses them to act
with the permissions of their Atlassian accounts. Credential disclosure,
credential delivery to a non-Atlassian origin, or acting through the wrong
profile is a security issue.

## Reporting a vulnerability

Report privately through GitHub:
[Report a vulnerability](https://github.com/abigotado/confluence-cli/security/advisories/new).
Do not open a public issue with credential material or an exploitable path.

## Never include a credential

A redacted reproduction is sufficient. Never attach an API token, an
Authorization header, Keychain output, environment dump, or credential-bearing
request trace. If a token may have been exposed, revoke it in Atlassian account
settings before reporting the incident.

## Security boundaries

- Tokens are accepted through bounded stdin and stored only in macOS Keychain.
- The non-secret profile registry is private (`0700` directory, `0600` file).
- The non-secret write-policy registry has the same permissions and binds exact
  space IDs to the profile's credential generation and declared capabilities.
- Every network call requires an explicit `--profile`; no active-profile switch
  exists.
- Production HTTP requests refuse redirects and restrict credential-bearing
  origins to validated Atlassian hosts.
- Keychain access uses Security.framework directly with UI disabled. The
  project never invokes the `security` command to read credentials.
- On Unix, the skill installer checks mode bits for the destination and all its
  ancestors, refusing components writable by the group or other users. A
  sticky shared ancestor is allowed only above an already-existing private
  component; it cannot itself be the install destination. ACL-shared
  destinations are unsupported.
  Processes running as the same OS user remain inside the local trust boundary;
  do not target directories controlled by untrusted same-user automation.
- Tests use fake stores and local HTTP servers; they never read or mutate the
  real Keychain.
- Page dry-runs read only non-secret local profile metadata, bind confirmation
  to the exact profile identity, and omit body content. Versioned Keychain
  payloads bind tokens to the full profile identity, credential generation,
  and capabilities; mismatches
  block every network path. Confirmed writes use typed payloads, refuse redirects and retries, and dispatch once. An uncertain
  post-dispatch outcome is `WRITE_OUTCOME_UNKNOWN` and must be reconciled, not
  automatically repeated.

Compromise of the local user account, macOS itself, Confluence Cloud, or Atlassian's
permission model is outside this project's security boundary.
