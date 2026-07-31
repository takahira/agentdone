package handler

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/takahira/agentdone/internal/slack"
	"github.com/takahira/agentdone/internal/state"
)

// Rune budgets for the notification fields. Runes (not bytes) so multi-byte
// Japanese text is cut on character boundaries.
const (
	// maxPromptRunes bounds the prompt/question label on every notification type.
	maxPromptRunes = 60
	// maxExcerptRunes bounds the assistant-message excerpt on Stop (completion
	// and confirmation alike).
	maxExcerptRunes = 140
	// maxFieldRunes bounds a single-value field: error text, notification
	// content, PreToolUse question.
	maxFieldRunes = 200
)

// appendContext writes the session/prompt/location block shared by the
// Notification, PreToolUse and StopFailure notifications. Stop builds its own
// header instead (it folds location into the model/output/skill meta line), so
// it does not use this helper.
func appendContext(b *strings.Builder, m messages, turn state.Turn, cwd string) {
	if turn.SessionTitle != "" {
		fmt.Fprintf(b, "%s\n", m.line(m.session, turn.SessionTitle))
	}
	if turn.Prompt != "" {
		fmt.Fprintf(b, "%s\n", m.line(m.prompt, truncate(oneLine(turn.Prompt), maxPromptRunes)))
	}
	if loc := location(cwd); loc != "" {
		fmt.Fprintf(b, "%s\n", m.line(m.location, loc))
	}
}

// oneLine flattens a value to a single line so it can't break the notification's
// field layout: CR/LF/tab all become a space.
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == '\t' {
			return ' '
		}
		return r
	}, s)
}

// truncate shortens s to at most n runes (not bytes), so multi-byte Japanese
// text is cut on character boundaries.
//
// Secrets are masked BEFORE the cut: slack.Post redacts again at egress, but
// several of its patterns carry minimum-length floors, and cutting a token
// mid-way can drop the remainder below a floor so a partial credential slips
// through. Redacting the intact string first closes that gap; at worst the cut
// leaves a partial mask, never a partial secret.
func truncate(s string, n int) string {
	r := []rune(slack.RedactSecrets(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n])
}

// truncateHead keeps the LAST n runes of s (the head is dropped). Used for
// confirmation excerpts, where the question sits at the end of the message.
// Redacts before the cut for the same reason as truncate: dropping a token's
// head (its recognisable prefix) would leave an unmatchable tail in the clear.
func truncateHead(s string, n int) string {
	r := []rune(slack.RedactSecrets(s))
	if len(r) <= n {
		return string(r)
	}
	return "…" + string(r[len(r)-n:])
}

// formatTokens renders a token count, rounded to one decimal: 1500 -> "1.5k",
// 1099 -> "1.1k", 1950 -> "2.0k".
func formatTokens(n int64) string {
	if n >= 1000 {
		d := (n + 50) / 100 // tenths of a thousand, rounded to nearest
		return fmt.Sprintf("%d.%dk", d/10, d%10)
	}
	return strconv.FormatInt(n, 10)
}
