package handler

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/takahira/agentdone/internal/config"
	"github.com/takahira/agentdone/internal/slack"
	"github.com/takahira/agentdone/internal/state"
	"github.com/takahira/agentdone/internal/transcript"
	"github.com/takahira/agentdone/pkg/cchooks"
)

// defaultErrorCooldownSeconds: don't re-report the SAME error for the same
// session within this window. During a rate-limit window every queued
// background wake dies on the identical error within seconds of waking — one
// ping per pending task, observed in the wild — and the first ping carries all
// the signal. Override with AGENTDONE_ERROR_COOLDOWN (seconds); 0 notifies on
// every failure. A completed turn clears the window early (see Stop).
const defaultErrorCooldownSeconds = 1800

func errorCooldownSeconds() int64 {
	v := os.Getenv("AGENTDONE_ERROR_COOLDOWN")
	if v == "" {
		return defaultErrorCooldownSeconds
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return int64(n)
	}
	// Same contract as AGENTDONE_THRESHOLD: an invalid value ("30m", a negative)
	// silently falls back to the default; surface it under AGENTDONE_DEBUG (and
	// `doctor` prints the effective value). The unit is seconds, integers only.
	if os.Getenv("AGENTDONE_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "agentdone: ignoring invalid AGENTDONE_ERROR_COOLDOWN %q (want a non-negative integer of seconds); using %d\n", v, defaultErrorCooldownSeconds)
	}
	return defaultErrorCooldownSeconds
}

// StopFailure handles a turn that ended on an API error (rate limit, overload,
// auth, …). It notifies without Stop's duration threshold — an error matters
// regardless of how long the turn ran — but a repeat of the SAME error within
// errorCooldownSeconds is muted (see the constant above).
//
// Peek, don't consume: an errored turn is often NOT the session's end. Pending
// background tasks will wake later turns, and the eventual real completion
// still needs this turn's prompt / start time (the wake's own synthetic
// UserPromptSubmit is deliberately not saved — see UserPromptSubmit). Leftover
// state is reclaimed by the next real prompt's Save or the stale sweep.
func StopFailure(in *cchooks.StopFailure) {
	// Hold a per-session lock across check -> POST -> save. Two concurrent async
	// StopFailure processes otherwise both see "no recent note" before either has
	// written one and both notify -- in precisely the queued-wakeup storm the
	// cooldown exists to collapse. The lock fails open (see WithFailureLock), so
	// the re-check below is what collapses the race in that case.
	// The cooldown re-check lives INSIDE fn, so it also covers the fail-open
	// path: a loser that waited out lockWait re-reads the note the winner saved
	// meanwhile and suppresses itself.
	state.WithFailureLock(in.SessionID, func() {
		stopFailureLocked(in)
	})
}

func stopFailureLocked(in *cchooks.StopFailure) {
	turn, _ := state.Peek(in.SessionID, in.PromptID)
	errText := in.ErrorText()
	if suppressed(in.SessionID, errText) {
		return
	}

	if turn.SessionTitle == "" {
		// stdin session_title is unreliable; fall back to ai-title. Only the title
		// is needed here, so skip the full Aggregate (token/start work).
		turn.SessionTitle = transcript.LatestTitle(in.TranscriptPath)
	}

	m := activeMessages()
	var b strings.Builder
	b.WriteString(m.errorEnd + "\n")
	appendContext(&b, m, turn, in.Cwd)
	if errText != "" {
		fmt.Fprintf(&b, "%s", m.line(m.errLabel, truncate(oneLine(errText), maxFieldRunes)))
	}
	// Record the note only after a real delivery.
	//
	// Two ways a post "succeeds" without anything being delivered:
	//   - no webhook is configured at all -- slack.Post returns nil for an empty
	//     URL. Recording a note then meant a user who hits an error, THEN
	//     configures .webhook, gets silence for the next 30 minutes on the very
	//     error they set it up for.
	//   - AGENTDONE_STDOUT=1, where stdout IS the delivery. That one counts.
	url := config.WebhookURL()
	delivered := slack.Post(url, strings.TrimRight(b.String(), "\n")) == nil
	if delivered && (url != "" || os.Getenv("AGENTDONE_STDOUT") == "1") {
		_ = state.SaveFailure(in.SessionID, state.FailureNote{Epoch: state.Now(), Error: errText})
	}
}

// suppressed reports whether this failure repeats the last NOTIFIED error for
// the session within the cooldown window. Only an identical error is muted — a
// different error is a different problem and always notifies — and a note from
// the future (the wall clock stepped backwards) never suppresses: failing open
// to one extra ping beats muting a real error on a meaningless comparison.
func suppressed(sessionID, errText string) bool {
	cool := errorCooldownSeconds()
	if cool == 0 {
		return false
	}
	note, ok := state.PeekFailure(sessionID)
	if !ok || note.Error != errText {
		return false
	}
	since := (state.Now() - note.Epoch) / 1000
	if since < 0 || since >= cool {
		return false
	}
	if os.Getenv("AGENTDONE_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "agentdone: repeat of error %q %ds after the last ping (cooldown %ds); not re-notifying\n", errText, since, cool)
	}
	return true
}
