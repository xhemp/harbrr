# Security Policy

harbrr is self-hosted software that stores sensitive credentials — tracker passkeys, cookies,
API keys, and download tokens, plus its own web-UI login and management-API keys. We take
security reports seriously.

## Supported versions

harbrr is in **alpha**. Security fixes land on the latest `main` / most recent release only;
there are no back-ported patch releases during alpha.

## Reporting a vulnerability

**Please do not open a public issue for a security vulnerability.**

Report it privately via GitHub's **Private vulnerability reporting** — the **"Report a
vulnerability"** button under the repository's **Security** tab
([direct link](https://github.com/autobrr/harbrr/security/advisories/new)). This opens a
private advisory visible only to you and the maintainers.

Where possible, include:

- A description of the issue and its impact.
- Steps to reproduce (a proof of concept, the affected endpoint/config).
- The harbrr version/commit, how it was deployed (Docker, binary), and any relevant
  configuration (auth mode, reverse proxy, secrets backend).

## What to expect

- We aim to acknowledge a report within a few days and keep you posted on progress.
- We'll work with you on a fix and coordinate a disclosure timeline — please give us
  reasonable time to ship a fix before any public disclosure.
- We're glad to credit reporters in the advisory/release notes unless you'd rather remain
  anonymous.

## Scope

harbrr is **self-hosted**: the security boundary is the operator's own deployment, and the
most sensitive areas are credential handling and the management API. By design:

- Tracker credentials are encrypted at rest (AES-256-GCM); the admin password and API keys
  are hashed, never stored in a recoverable form.
- Secrets are redacted from logs, errors, and traces, and a tracker passkey never appears in
  the served Torznab feed (download links are resolved server-side).

The full model is in [`docs/security.md`](docs/security.md).

Reports about an operator's own misconfiguration (e.g. running with `allow_plaintext`, or
exposing the management API without auth) aren't vulnerabilities in harbrr — but we're happy
to improve docs and guardrails where it helps.
