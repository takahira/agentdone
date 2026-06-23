# agentdone

**English** | [日本語](README-ja.md)

**Slack notifications for Claude Code that only fire when the agent is *really* done.**

A single static binary (no `jq` / Node / Python) that hooks into Claude Code and
sends low-noise, context-rich notifications — and, crucially, **withholds the
premature "completed" ping while background work is still running**.

[![MIT License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go 1.24+](https://img.shields.io/badge/go-1.24%2B-blue.svg)](https://go.dev)
[![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey.svg)](https://github.com/takahira/agentdone)

---

## Quick start

```sh
curl -fsSL https://raw.githubusercontent.com/takahira/agentdone/main/install.sh | sh
agentdone init   # wires the hooks + prompts once for your Slack webhook
```

That's it — the next time a turn really finishes, you get one clean ping. No
config file; tune it with [environment variables](#configuration-environment-variables)
if you want. Build-from-source and the full story are under [Install](#install).

---

## The problem

When you run background tasks (a parallel sweep, a Workflow, a long `&` job) and
the turn ends to wait for them, Claude Code injects a "background command
completed" message — and naive notification hooks fire a **false "✅ completed"**
even though the agent is still working. This pollutes your Slack and your
attention. (See [anthropics/claude-code#18544](https://github.com/anthropics/claude-code/issues/18544)
and friends.)

`agentdone` reads the official `background_tasks` field that Claude Code now
puts on the `Stop` hook, and stays silent while anything is in flight — then
sends **one** correct notification when the work is genuinely done.

---

## What makes it different

Most Claude Code notifiers hook `Stop` and fire immediately. The catch: `Stop`
*also* fires while background work is still running, so they send a **false
"✅ completed"** the moment a turn pauses to wait. agentdone is built around
suppressing exactly that:

| | False-completion suppression | Context (model · tokens · skill) | Runtime deps |
| --- | :---: | :---: | :---: |
| Generic `Stop`-hook notifier | ❌ | usually none | varies |
| **agentdone** | ✅ | ✅ | none (one static binary) |

- You run Claude Code (often with parallel/background work) and want a Slack ping
  **only when it's actually done**, with enough context to know which session.

It is **not** trying to be an all-in-one notifier (Discord/ntfy/desktop, dashboards
— other tools do that well). It's a focused, honest take on *low-noise* hook
notifications, and a worked example of using the newer `background_tasks` /
`last_assistant_message` hook fields.

---

## Before / after

```text
1) A background sweep is still running, and the turn ends to wait for it.
   Naive hooks fire a false "completed" ping right here. agentdone stays silent:
   $ echo '<Stop, background_tasks:[sweep running]>' | agentdone
   → (no notification — withheld) ✅

2) The sweep finished; the agent is really done.
   $ echo '<Stop, background_tasks:[]>' | agentdone
   ---
   :white_check_mark: Done (10m 0s)
   Session: Refactor the parser
   Prompt: run the tests in parallel
   Where: myproject (main) · Model: opus-4-8 · Output: 27.3k tok · Skill: code-review
   Did: Aggregated the parallel run and reported back.
```

Run it yourself — this builds from source (needs a Go toolchain) and prints to
your terminal, no Slack or webhook required:

```sh
sh demo/demo.sh
```

### Example notifications

The other notification types render the same way (these are real
`AGENTDONE_STDOUT=1` outputs; Slack shows the emoji, e.g. `:x:` → ❌):

```text
:x: Ended on error
Session: Refactor the parser
Prompt: run the tests in parallel
Where: myproject (main)
Error: rate_limit: rate limit exceeded; retry after 30s
```

```text
:raised_hand: Waiting for confirmation (a multiple-choice question)
Session: Refactor the parser
Prompt: run the tests in parallel
Where: myproject (main)
Question: Commit these changes to main?
```

---

## Requirements

- **Claude Code** — v2.1.145+ for the official `background_tasks` field the
  suppression relies on.
- A **Slack Incoming Webhook** URL.
- Permission / idle alerts need a **terminal** `claude` session — they do not fire
  in the VS Code extension ([details](#where-each-notification-fires-vs-code-extension-vs-terminal)).
  Completion and confirmation pings work everywhere.

---

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/takahira/agentdone/main/install.sh | sh
```

The script verifies each download's SHA-256 against the release `checksums.txt`
before installing — prefer to inspect first? Read
[`install.sh`](install.sh), or see [Verifying a release](#verifying-a-release).

This downloads the single binary into `~/.claude/bin`, prompts once for your
Slack Incoming Webhook URL (stored at `~/.claude/hooks/.webhook`, `chmod 600` —
never in the repo or the binary), and wires the hooks into
`~/.claude/settings.json` (idempotently, preserving anything already there).

Set the webhook later instead via `SLACK_WEBHOOK_URL` or that file, then
`agentdone init`.

`agentdone uninstall` removes the hook wiring from `settings.json` (other
tools' hooks are untouched) and this tool's saved turn state. The binary
itself, `~/.claude/hooks/.webhook` and the `settings.json.bak` backup are left
for you to delete.

With a Go toolchain you can build from source instead, then wire it up:

```sh
go install github.com/takahira/agentdone/cmd/agentdone@latest
agentdone init
```

---

## Troubleshooting

- **Check the wiring first.** `agentdone doctor` diagnoses the wiring, webhook,
  state, language and effective threshold without sending anything (it never
  contacts Slack).
- **No pings arriving mid-session?** Set **`AGENTDONE_DEBUG=1`** — each hook then
  logs to stderr (which Claude Code captures) why it stayed silent. This catches
  the cases `doctor` can't see: a delivery failure, an invalid webhook, or a
  state-dir problem during the session.
- **Permission / idle alerts missing in VS Code?** Expected — that hook only fires
  in a terminal session ([details](#the-notification-hook-does-not-fire-in-the-vs-code-extension)).

---

## Configuration (environment variables)

agentdone is configured entirely through environment variables — no config file.

| Variable | Default | Effect |
| --- | --- | --- |
| `SLACK_WEBHOOK_URL` | *(unset)* | The Slack Incoming Webhook. If unset, read from `$CLAUDE_CONFIG_DIR/hooks/.webhook`. |
| `AGENTDONE_THRESHOLD` | `300` | Minimum turn length (seconds) to report a completion. `0` reports every completion — see [What it notifies](#what-it-notifies). |
| `AGENTDONE_LANG` | *(auto)* | `en` (default) or `ja`. Always wins over the POSIX locale (`LC_ALL` > `LC_MESSAGES` > `LANG`). |
| `AGENTDONE_DEBUG` | *(unset)* | When set, each hook logs to stderr why it stayed silent. |
| `AGENTDONE_STDOUT` | *(unset)* | When `1`, prints the notification instead of POSTing to Slack (used by the demo). |
| `CLAUDE_CONFIG_DIR` | `~/.claude` | Base dir for settings, the webhook file and turn state — same as Claude Code. |

The `install.sh` one-liner additionally honours `AGENTDONE_REPO`,
`AGENTDONE_BINDIR` and `AGENTDONE_VERSION` for the download source, install
directory and pinned version.

---

## Verifying a release

Release archives are built and published by this repository's CI and carry a
[GitHub build-provenance attestation](https://docs.github.com/actions/security-guides/using-artifact-attestations-to-establish-provenance-for-builds).
`install.sh` already checks each download's SHA-256 against the release
`checksums.txt`; for a stronger, identity-bound guarantee, verify the attestation
with the GitHub CLI:

```sh
gh attestation verify agentdone_<os>_<arch>.tar.gz \
  --repo takahira/agentdone
```

This confirms the archive was produced by this repo's release workflow — not just
that it matches a checksum file an attacker could also replace.

Changes between versions are in the
[GitHub Releases](https://github.com/takahira/agentdone/releases)
notes.

---

## What it notifies

| When | Notification |
| --- | --- |
| Turn finished (≥ 300 s by default, or a plain-text confirmation question) | `✅ Done` / `✋ Waiting for confirmation` with session title, prompt, repo·branch, model, output tokens, skill, and a one-line summary |
| Turn ended on an API error (rate limit, overload, auth, …) (`StopFailure`) | `❌ Ended on error` with session context and the error — always sent, regardless of duration |
| Background work still running at turn end | *(nothing — withheld)* |
| Permission / idle prompt (`Notification`) | `✋ Waiting for permission` / `✋ Waiting for input` — *terminal only, see below* |
| `AskUserQuestion` / `ExitPlanMode` (`PreToolUse`) | `✋ Waiting for confirmation` with the question / plan excerpt |

`AGENTDONE_THRESHOLD` (seconds) tunes the completion floor. Setting it to `0`
reports every completion, including turns whose start time is unknown — which any
non-zero threshold withholds (see [Known limitations](#known-limitations)).

Notification text defaults to **English**; set `AGENTDONE_LANG=ja` for Japanese.
`AGENTDONE_LANG` always wins; otherwise the POSIX locale is consulted in order
(`LC_ALL`, then `LC_MESSAGES`, then `LANG`), so `LANG=ja*` selects Japanese only
when `LC_ALL` / `LC_MESSAGES` are unset.

---

## How it works

- **Most context comes straight from hook stdin** — `last_assistant_message`,
  `background_tasks`, `notification_type`. The transcript is parsed only for the
  residuals: output-token total, model, skill, the session title (stdin's
  `session_title` is unreliable, so it falls back to the transcript's ai-title),
  and the start-epoch correction for a task-woken turn (one resumed to report a
  finished background task, with no fresh `UserPromptSubmit`).
- The Claude Code hook payload types live in a small, self-contained package:
  [`pkg/cchooks`](pkg/cchooks) — Go types for **all 30 hook events**
  (reverse-engineered from the claude-code binary, last verified against
  **v2.1.168** — `agentdone doctor` prints the verified version), decoded with
  `cchooks.Parse` / `cchooks.Decode`. It depends only on the standard library, so
  other Go hooks can import it directly — though it ships as part of this module,
  not as a separately versioned library.

---

## Where each notification fires (VS Code extension vs terminal)

Claude Code exposes several hook events, and *which* ones actually fire depends on
how you run it. The "Claude is waiting for you" cases are split across three
events on purpose, so you don't miss them even when one is unavailable:

| You're waiting because… | Hook event | VS Code ext? | Terminal? |
| --- | --- | --- | --- |
| Claude asked a plain-text question and stopped (e.g. "commit this?") | `Stop` (detected as a question) | ✅ | ✅ |
| Claude used `AskUserQuestion` / `ExitPlanMode` | `PreToolUse` | ✅ | ✅ |
| Permission prompt / idle prompt | `Notification` | ❌ | ✅ |

### The `Notification` hook does not fire in the VS Code extension

Observed against claude-code **v2.1.156** (my own testing): in the VS Code native extension the
`Notification` hook event is **never emitted** — for *any* sub-type
(`permission_prompt`, `idle_prompt`, `auth_success`, `elicitation_*`). The
extension's native UI handles idle/permission in its own surface without emitting
the hook event. This matches [anthropics/claude-code#8985](https://github.com/anthropics/claude-code/issues/8985).

Evidence from a long extension session (the hook debug log):

- **0** `Notification` events were recorded, despite (a) idling well past the
  60 s `idle_prompt` threshold and (b) an actual permission dialog appearing
  (and being denied) — neither fired the hook.
- Meanwhile `Stop` (86×), `UserPromptSubmit` (86×) and `PreToolUse` (14×) all
  fired normally in the same session.

**This is fine in practice.** The two common confirmation cases — plain-text
questions and `AskUserQuestion` / `ExitPlanMode` — ride on `Stop` and
`PreToolUse`, which *do* fire in the extension, so you still get a
`✋ Waiting for confirmation` ping (sent by the `stop` / `pretooluse` handlers,
not `notification`). In a
**terminal** session the `Notification` handler also fires and adds the
permission / idle alerts on top.

---

## Known limitations

- The hook's `background_tasks` element has **no flag** for whether a task was
  pushed aside with Ctrl+B. So a long-running task you backgrounded with Ctrl+B
  will keep suppressing completion pings until it ends (matching the official
  "session is paused waiting for background work" semantic). Distinguishing it
  would need transcript-based "launched this turn" scoping.
- A completion ping needs a known turn start — from the turn's `UserPromptSubmit`,
  or recovered from the transcript for a task-woken turn (one resumed to report a
  finished background task). In the uncommon case where neither is available (the
  hook was added mid-session, or after `/clear` / resume), a long turn may be
  **withheld entirely** rather than sent with an unknown duration — a deliberate
  trade-off to avoid spamming short turns. Confirmation questions are exempt (they
  always notify), and so is `AGENTDONE_THRESHOLD=0`, which asks for every completion.
- The `Notification` hook (permission / idle alerts) does not fire in the VS Code
  extension — see [Where each notification fires](#the-notification-hook-does-not-fire-in-the-vs-code-extension).
- Slack only, for now. macOS/Linux primarily. The `install.sh` one-liner is
  POSIX-sh and does not run on Windows; Windows binaries are built and published
  (download `agentdone_windows_<arch>.zip` from a release and run `agentdone init`)
  but are less exercised.
- agentdone notifies **once per turn** — via the parent `Stop`, which already
  withholds until background work finishes. Per-task events (`SubagentStop`,
  `TaskCompleted`, …) are intentionally *not* wired: a fan-out of N subagents
  would otherwise fire N pings. Those subagents' output tokens are folded into
  the turn's reported total.
- Notification text is **English by default**, with Japanese available via
  `AGENTDONE_LANG=ja`. The POSIX locale (`LC_ALL` > `LC_MESSAGES` > `LANG`) is
  the fallback, so `LANG=ja*` only selects Japanese when `LC_ALL` / `LC_MESSAGES`
  are unset. Adding another language is one more entry in the message catalog
  (`internal/handler/lang.go`).
- Paths follow **`$CLAUDE_CONFIG_DIR`** when set (falling back to `~/.claude`),
  the same as Claude Code — so settings, the webhook file and turn state stay
  wherever you point Claude Code.

---

## Running tests

```sh
go test -race ./...   # 92 test functions; CI runs this with -race
```

---

## Status

Used daily, installable, and release-signed — a focused **personal / reference
tool** rather than a staffed project: issues and pull requests are welcome and
triaged as time allows. The name is the whole idea: *notify me only when the
agent is done.*

---

## License

MIT
