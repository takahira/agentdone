# Security Policy

## Reporting a vulnerability

Please report security issues **privately** via GitHub's private vulnerability
reporting: the repository's **Security** tab → **Report a vulnerability**.
Please do not open a public issue for security problems.

## Notes on the security model

- **The Slack webhook URL is a secret.** It is read from the `SLACK_WEBHOOK_URL`
  environment variable or from `~/.claude/hooks/.webhook` (created `chmod 600`).
  It is never committed to the repository or compiled into the binary.
- **Install integrity.** `install.sh` downloads the release archive and verifies
  its SHA-256 against the release's published `checksums.txt` before installing.
  Release archives also carry a GitHub build-provenance attestation, verifiable
  with `gh attestation verify <archive> --repo takahira/agentdone`.
- **No network listeners.** Hooks run as short-lived local processes that only
  make outbound HTTPS POSTs to the configured Slack webhook.
- **Untrusted input.** Hook payloads come from the local Claude Code process and
  are size-capped before parsing; Slack message bodies are JSON-encoded, never
  string-concatenated.
