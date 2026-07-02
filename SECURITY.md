# Security Policy

## Supported versions

Only the latest minor release is supported. Older releases do not receive security patches — upgrade to the current version to stay covered.

## Reporting a vulnerability

**Do not report security issues via public GitHub issues.** Open a private security advisory at https://github.com/MattermostFederal/mattermost-plugin-channel-requests/security/advisories/new and one of the maintainers listed in [CODEOWNERS](./CODEOWNERS) will respond within five business days.

If the reporting form is unavailable, email the primary maintainer (see `git log` for the current active contact). Please include:

- Plugin version affected
- Mattermost server version + deployment shape (docker, k8s, bare-metal)
- Reproducer or PoC
- Suggested severity + remediation ideas if you have them

## Supply-chain artifacts

Every release ships with the following artifacts under the `security/` directory of the release tarball AND as separate assets on the GitHub Release:

- **CycloneDX SBOM** (`sbom/server-sbom.json` + `sbom/webapp-sbom.json`) — full dependency graph for the shipped Go binary + npm bundle. Verifiable with `grype sbom:server-sbom.json`.
- **CodeQL SARIF** (`security/codeql-go.sarif`) — static-analysis findings from the release build. Uploaded to the repo's GitHub Code Scanning tab.
- **SHA256 checksum** (`<bundle>.sha256`) — verify with `sha256sum -c <bundle>.tar.gz.sha256`.
- **GPG signature** (`<bundle>.sig`, when the release was signed) — verify with `gpg --verify <bundle>.sig <bundle>.tar.gz`.

## Release pipeline gating

Releases are built by a security-gated CI pipeline (`.github/workflows/release.yml`) that fails on:

- Any Grype-detected HIGH or CRITICAL CVE in the shipped dependency tree
- Any CodeQL error-level finding
- Any ClamAV virus signature match in the release artifacts

A release cannot be published without passing all three gates.

## Development-time scanning

Contributors are expected to run local security checks before submitting a PR:

```sh
make sbom-audit     # SBOM + Grype scan
make codeql-analyze # CodeQL static analysis
make security-gate  # fail on critical/high SARIF findings
```

The same checks run on every pull request via `.github/workflows/security.yml`. PRs cannot merge to `main` while any of the required security checks are red.

## Handling suppression

Legitimate false-positive CVEs (dev-only transitive, upstream-fixed but not yet released, etc.) can be suppressed in `.grype.yaml` with a `reason:` string. Reviewers expect every suppression to be time-bounded — add a `# TODO: revisit YYYY-MM-DD` comment so suppressions get re-evaluated instead of ossifying.
