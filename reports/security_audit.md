# Security Audit Report

## 1. Security Scoring Breakdown

- Sensitive File Protection: 100/100 (Strong), weight 25%
- Secret Detection: 100/100 (Strong), weight 30%
- Dependency Security: 95/100 (Strong), weight 20%
- Supply Chain Integrity: 100/100 (Strong), weight 10%
- Security Automation & CI/CD: 60/100 (Weak), weight 15%
- Overall Score: 93/100 (Strong)
- Formula: round(100*0.25 + 100*0.30 + 95*0.20 + 100*0.10 + 60*0.15) = 93
- Security Posture: Secure

## 2. Executive Summary

- Overall Score: 93/100 (Strong)
- The read MVP and guarded page-write surface have no known reachable or module-level vulnerabilities after upgrading `golang.org/x/sys` to the first fixed release for `GO-2026-5024`.
- No token, key, credential file, or hardcoded secret was found. Gitleaks reported zero findings with redaction enabled.
- The strongest controls are bounded no-echo token input, the native fail-no-UI Keychain boundary, atomic token/profile-identity/generation/capability binding, non-serializable credentials, fixed Atlassian gateway, refused redirects, fail-closed pagination, generation-bound space allowlists, local-only dry-run, profile-identity-bound write confirmation, non-replayable one-shot mutations, ambiguous-outcome handling, immutable GitHub Action pins, and the generated machine contract.
- The weakest scored area is automation because there is no second filesystem scanner or pre-commit secret scanner. Pinned Govulncheck, module checksum verification, Dependabot, race tests, and PR CI are present.
- Priority recommendation: keep delete, admin, bulk, raw-JSON, automatic write retries, and unapproved publication outside the contract. Migrate the deprecated Keychain no-UI constant to `LAContext.interactionNotAllowed` only when equivalent no-prompt behavior remains proven.

## 3. Security Automation & CI/CD

- Description: Evaluates automated dependency updates, vulnerability scanning, pull-request checks, checksum verification, secret hooks, and additional scanners.
- Score: 60/100 (Weak)
- Score Breakdown:
  - Base: 0
  - Dependabot configured: +20
  - CI vulnerability scanning with pinned Govulncheck: +20
  - CI runs on pull requests: +10
  - Module checksum validation in CI: +10
  - Pre-commit security hook: +0
  - Additional filesystem scanner: +0
  - Final: 60/100 (Weak)
- Key Findings:
  - [LOW]: No Trivy or equivalent second filesystem scanner is configured.
  - [LOW]: No pre-commit Gitleaks hook or repository `.gitleaks.toml` is configured.
  - [LOW]: Dependabot covers both Go modules and GitHub Actions.
- Evidence:
  - `.github/workflows/go.yaml:7-13` runs CI on pushes and pull requests.
  - `.github/workflows/go.yaml:45-55` verifies checksums and runs pinned Govulncheck.
  - `.github/dependabot.yml:3-27` schedules Go-module and Actions updates.
  - `reports/.artifacts/step_07_security_trivy.md` records the optional scanner skip.
- Risks: A detector-specific false negative could remain unnoticed until another scanner or manual review finds it.
- Recommendations:
  1. Keep Govulncheck pinned and fail CI on reachable or module vulnerabilities.
  2. Consider a redacted Gitleaks CI or pre-commit check after the repository is published.
  3. Add a second scanner only with a pinned, maintainable configuration and low false-positive burden.

## 4. Dependency Security

- Description: Evaluates known vulnerabilities, dependency age, deprecation, lock integrity, and direct dependency maintenance.
- Score: 95/100 (Strong)
- Score Breakdown:
  - Base: 100
  - Critical/high/medium/low CVEs after remediation: 0
  - More than 5 modules with newer releases: -10
  - Deprecated packages: 0
  - `go.sum` integrity hashes verified: +5
  - Final: 95/100 (Strong)
- Key Findings:
  - [LOW]: Six direct or transitive modules have newer releases; the selected `x/sys` and `x/term` versions are the newest compatible security baseline used here without raising the module above Go 1.25.
  - [LOW]: The initial `x/sys v0.33.0` had a non-reachable Windows advisory and was upgraded to fixed `v0.44.0` during this audit.
- Dependency Age Analysis:
  - Outdated count: 6
  - Deprecated count: 0
  - Top updates: `pflag` 1.0.9 -> 1.0.10, `go-md2man` 2.0.6 -> 2.0.7, `x/sys` 0.44.0 -> 0.47.0, and `x/term` 0.43.0 -> 0.45.0.
- Evidence:
  - `go.mod:3-13` contains the Go baseline and small direct dependency set.
  - `reports/.artifacts/step_05_security_dependency_audit.md` records both Govulncheck runs and remediation.
  - `reports/.artifacts/step_06_security_dependency_age.md` records current-version comparisons.
- Risks: Blindly taking every newer module could unnecessarily raise the Go baseline; deferring all updates could miss security fixes.
- Recommendations:
  1. Let Dependabot propose compatible updates and require the full race/release-policy CI gate.
  2. Re-run Govulncheck before any tag or Homebrew Formula is created.
  3. Treat a future Go baseline increase as an explicit compatibility decision.

## 5. Secret Detection

- Description: Evaluates hardcoded tokens and credentials, secret-pattern scans, Git-history leaks, and accidental rendering paths.
- Score: 100/100 (Strong)
- Score Breakdown:
  - Base: 100
  - High/medium/low hardcoded-secret findings: 0
  - Git-history findings: 0
  - Pre-commit secret-hook bonus: +0
  - `.gitleaks.toml` bonus: +0
  - Final: 100/100 (Strong)
- Key Findings:
  - [LOW]: No residual secret finding was detected; authentication material exists only as bounded in-memory values and native Keychain data.
- Evidence:
  - `internal/auth/store.go:32-48` excludes credentials from JSON and redacts formatting.
  - `internal/confluence/client.go:176-192` constructs Basic auth in memory without logging the header.
  - Guarded-write receipts expose body byte counts and SHA-256 digests, never bodies, titles, tokens, or authorization headers.
  - HTTP credential tests cover both JSON and structured-log redaction.
  - `reports/.artifacts/step_03_security_secret_patterns.md` reports zero pattern findings.
  - `reports/.artifacts/step_04_security_gitleaks.md` reports zero working-tree and history findings.
- Risks: Future debugging changes could leak credential-bearing headers unless the no-header-logging invariant remains tested and reviewed.
- Recommendations:
  1. Retain sentinel-token tests for stdout, stderr, errors, and logs.
  2. Never add argv or environment-variable token input.
  3. Keep Gitleaks redaction enabled in future automation.

## 6. Sensitive File Protection

- Description: Evaluates environment files, keys, cloud credentials, ignore coverage, and local metadata permissions.
- Score: 100/100 (Strong)
- Score Breakdown:
  - Base: 100
  - Tracked environment files: 0
  - Tracked keys/certificates/cloud credentials: 0
  - Missing environment ignore coverage: 0
  - Optional safe `.env.example` bonus: +0
  - Final: 100/100 (Strong)
- Key Findings:
  - [LOW]: No credential file exists; an `.env.example` is intentionally absent because environment credentials are not supported.
- Evidence:
  - `.gitignore:20-23` ignores `.env`, `.env.*`, and macOS metadata.
  - `reports/.artifacts/step_02_security_file_analysis.md` records no sensitive files and no tracked environment files.
  - Profile registry implementation enforces a `0700` directory and `0600` non-secret JSON file.
- Risks: Adding an alternate plaintext credential path would bypass the native Keychain design.
- Recommendations:
  1. Reject proposals for token files, environment credentials, or repository-local secrets.
  2. Preserve strict registry permissions and unknown-field rejection.
  3. Keep generated provider harness outputs ignored because they contain machine-specific absolute paths.

## 7. Supply Chain Integrity

- Description: Evaluates official registries, checksums, git/path dependencies, release isolation, and artifact policy.
- Score: 100/100 (Strong)
- Score Breakdown:
  - Base: 100
  - Git/path/unknown-registry dependencies: 0
  - Missing lock/integrity hashes: 0
  - Official Go registry/module sources: +10
  - Verified checksums: +5
  - Final after clamping: 100/100 (Strong)
- Key Findings:
  - [LOW]: Release configuration is present but intentionally cannot publish macOS binaries; macOS is source-built so Security.framework remains the credential backend.
  - [LOW]: All third-party GitHub Actions are pinned to immutable commit SHAs with version comments.
  - [LOW]: Windows read-contract builds fail closed for skill mutation; hash-safe mutation remains macOS/Linux only.
- Evidence:
  - `go.mod:5-13` contains only registry-resolved modules and no replacements.
  - `.github/workflows/go.yaml:82-96` rehearses GoReleaser and rejects Darwin artifacts.
  - `.github/workflows/go.yaml`, `.github/workflows/release.yaml`, and `.github/workflows/agent_harness.yaml` pin every third-party Action to a 40-character commit SHA.
  - `.goreleaser.yaml` emits checksum-protected Linux and Windows archives only.
  - The local snapshot rehearsal produced no Darwin artifacts and performed no publication.
- Risks: Adding a prebuilt unsigned macOS archive or a Homebrew Formula before an immutable public tag and SHA would weaken the credential-backend and provenance guarantees.
- Recommendations:
  1. Create no Formula until a public immutable source tag and SHA-256 exist.
  2. Keep macOS installation source-built with `CGO_ENABLED=1`.
  3. Rehearse release policy locally and in CI before each publication approval.

## 8. Consolidated Findings by Severity

- HIGH: 0
- MEDIUM: 0
- LOW: 4
- [LOW]: Six modules have newer versions; Govulncheck is clean after the `x/sys` remediation.
- [LOW]: No additional filesystem scanner is configured.
- [LOW]: No repository pre-commit secret scanner is configured.
- [LOW]: `internal/auth/keychain_darwin.go:23` uses a functional but deprecated Apple fail-no-UI constant.

## 9. Remediation Priority Matrix

1. [LOW]: Before publication, repeat Govulncheck, Gitleaks with redaction, race tests, and the release snapshot rehearsal.
2. [LOW]: Migrate to `LAContext.interactionNotAllowed` only with tests proving Keychain operations still fail rather than show UI.
3. [LOW]: Evaluate compatible dependency updates through Dependabot and the complete CI gate.
4. [LOW]: Consider adding a pinned second scanner after measuring false positives and maintenance cost.

## 10. Gemini AI Analysis

- Status: skipped.
- Gemini CLI was not installed, and external AI analysis of this uncommitted working tree was not authorized.
- No repository source or scan artifact was transmitted to an external AI provider.

## 11. Project Detection Results

- Detected project type: Go single-module command-line application.
- Base path: repository root.
- Primary surfaces: Cobra CLI, native macOS Keychain CGO bridge and ACL migration, bounded Confluence HTTP client, profile and generation-bound write-policy registries, guarded page create/update, portable skill installer, and GitHub release automation.
- No Node, Flutter, Rust, Python application, Java, Swift application, or .NET manifest was detected.

## 12. Appendix: Evidence Index

- `reports/.artifacts/step_01_security_tool_installer.md`: project/tool detection.
- `reports/.artifacts/step_02_security_file_analysis.md`: sensitive-file and ignore analysis.
- `reports/.artifacts/step_03_security_secret_patterns.md`: source secret-pattern scan.
- `reports/.artifacts/step_04_security_gitleaks.md`: redacted Gitleaks results.
- `reports/.artifacts/step_05_security_dependency_audit.md`: Govulncheck, lock, and CI evidence.
- `reports/.artifacts/step_06_security_dependency_age.md`: version-age evidence.
- `reports/.artifacts/step_07_security_trivy.md`: optional scanner status.
- `reports/.artifacts/step_08_security_sast.md`: OWASP-pattern and auth-boundary review.
- `reports/.artifacts/step_09_security_gemini_analysis.md`: external analysis skip rationale.

## 13. Scan Metadata

- Timestamp: 2026-08-26T23:04:06Z
- Generated by: Somnio CLI vunknown
- Skill: security-audit
- Project: `github.com/abigotado/confluence-cli`
- Working tree: new local repository with no commits and no remote
- Live Confluence requests: none
- Keychain reads: none executed by the audit
- Publication actions: none
- CLI contract guardian: approved after targeted contract and concurrency fixes
- Primary review: approved after targeted auth/status recovery fixes
- Independent adversarial review: approved after credential-binding, intent-binding, and expiry hardening
