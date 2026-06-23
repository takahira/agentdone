package handler

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/takahira/agentdone/internal/config"
	"github.com/takahira/agentdone/internal/slack"
	"github.com/takahira/agentdone/internal/state"
	"github.com/takahira/agentdone/internal/suppress"
	"github.com/takahira/agentdone/internal/transcript"
	"github.com/takahira/agentdone/pkg/cchooks"
)

// defaultThresholdSeconds: only notify for turns at least this long, unless the
// turn ends on a confirmation question. Override with AGENTDONE_THRESHOLD
// (seconds); 0 means notify on every completion.
const defaultThresholdSeconds = 300

func thresholdSeconds() int64 {
	v := os.Getenv("AGENTDONE_THRESHOLD")
	if v == "" {
		return defaultThresholdSeconds
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return int64(n)
	}
	// A human-friendly "5m" / "30m" / a negative number silently falls back to
	// the default, so the user who meant "30 minutes" gets a 300 s threshold and
	// no hint why. Surface it under AGENTDONE_DEBUG (and `doctor` prints the
	// effective value); the unit is seconds, integers only.
	if os.Getenv("AGENTDONE_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "agentdone: ignoring invalid AGENTDONE_THRESHOLD %q (want a non-negative integer of seconds); using %d\n", v, defaultThresholdSeconds)
	}
	return defaultThresholdSeconds
}

// Stop handles the Stop hook. It withholds a premature "done" ping while
// background work we are waiting on is still running, and otherwise sends a
// completion (or, for a confirmation question, a waiting) notification.
func Stop(in *cchooks.Stop) {
	// Peek, don't consume: a Stop withheld below must leave the turn state intact
	// so the later completion (the turn the task wakes, which has no preceding
	// UserPromptSubmit) can still report プロンプト / start time.
	turn, _ := state.Peek(in.SessionID)
	isAsk := looksLikeQuestion(in.LastAssistantMessage)

	// Withhold the "done" ping while waiting on background work; the real
	// completion fires on the turn that the task wakes.
	waiting := suppress.WaitingOnBackground(in.BackgroundTasks)
	if waiting && !isAsk {
		return
	}
	// Consume the saved state only when the turn is truly over. If a confirmation
	// question ended the turn while background work is still running, keep it: the
	// task will wake a later Stop (with no UserPromptSubmit of its own) whose
	// completion still needs this turn's プロンプト / start time. DeleteIf is a
	// compare-and-delete (see its doc): a slow async Stop must not clobber state a
	// newer turn's UserPromptSubmit has already saved.
	if !waiting {
		state.DeleteIf(in.SessionID, turn)
	}

	sum := transcript.Aggregate(in.TranscriptPath, turn.StartEpoch)
	if turn.SessionTitle == "" {
		turn.SessionTitle = sum.SessionTitle // stdin session_title is unreliable; fall back to ai-title
	}
	// The saved start (the prompt time) wins. The transcript-derived correction
	// points at the task LAUNCH, which is later: preferring it would undercount
	// the turn and — for a background task that finished in under the threshold —
	// drop the completion entirely. It is a rescue for task-woken turns whose
	// state was lost, nothing more.
	start := turn.StartEpoch
	if start == 0 {
		start = sum.StartEpoch
	}
	// elapsed (seconds) is only meaningful with a known start (a turn that had a
	// UserPromptSubmit, or a task-woken turn we could correct). The clock is in
	// milliseconds, so convert once here; threshold and duration are in seconds.
	var elapsed int64
	if start != 0 {
		elapsed = (state.Now() - start) / 1000
		if elapsed < 0 {
			// The wall clock went backwards since the turn started (an NTP step
			// correction, a manual time change): the elapsed time is meaningless.
			// Treat the start as unknown rather than withhold a genuinely long turn
			// on a negative comparison — handled by the (start == 0) branch below.
			start, elapsed = 0, 0
		}
	}
	// A confirmation question always notifies. A normal completion notifies only
	// once it is at least the threshold long; with no known start we cannot
	// apply that floor, so we stay quiet rather than risk spamming short turns —
	// unless the user set AGENTDONE_THRESHOLD=0, which asks for every completion,
	// duration known or not.
	th := thresholdSeconds()
	if !isAsk && ((start == 0 && th > 0) || elapsed < th) {
		return
	}

	m := activeMessages()
	header := m.done
	if isAsk {
		header = m.waiting
	}
	_ = slack.Post(config.WebhookURL(), buildStopText(header, isAsk, elapsed, turn, in, sum))
}

func buildStopText(header string, isAsk bool, elapsed int64, turn state.Turn, in *cchooks.Stop, sum transcript.Summary) string {
	m := activeMessages()
	var b strings.Builder
	fmt.Fprintf(&b, "%s%s\n", header, m.duration(elapsed))
	if turn.SessionTitle != "" {
		fmt.Fprintf(&b, "%s\n", m.line(m.session, turn.SessionTitle))
	}
	if turn.Prompt != "" {
		fmt.Fprintf(&b, "%s\n", m.line(m.prompt, truncate(oneLine(turn.Prompt), maxPromptRunes)))
	}

	var meta []string
	if loc := location(in.Cwd); loc != "" {
		meta = append(meta, m.line(m.location, loc))
	}
	if sum.Model != "" {
		meta = append(meta, m.line(m.model, sum.Model))
	}
	if sum.OutputTokens > 0 {
		meta = append(meta, m.line(m.output, formatTokens(sum.OutputTokens)+" tok"))
	}
	if sum.Skill != "" {
		meta = append(meta, m.line(m.skill, sum.Skill))
	}
	if len(meta) > 0 {
		fmt.Fprintf(&b, "%s\n", strings.Join(meta, m.metaSep))
	}

	if summary := strings.TrimSpace(in.LastAssistantMessage); summary != "" {
		label := m.did
		excerpt := truncate(oneLine(summary), maxExcerptRunes)
		if isAsk {
			// The question lives at the END of the message (often after a long
			// summary), so a confirmation excerpt keeps the tail, not the head.
			label = m.confirm
			excerpt = truncateHead(oneLine(summary), maxExcerptRunes)
		}
		fmt.Fprintf(&b, "%s", m.line(label, excerpt))
	}
	return strings.TrimRight(b.String(), "\n")
}
