// Package transcript extracts the few values that are not available on hook
// stdin and still require parsing the JSONL transcript: the output-token total,
// the model, the attribution skill, the session title, and the corrected
// turn-start epoch for task-woken turns.
package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Summary holds the transcript-derived residuals for a turn.
type Summary struct {
	OutputTokens int64
	Model        string
	Skill        string
	SessionTitle string // latest ai-title (hook stdin's session_title is unreliable)
	StartEpoch   int64  // corrected start for task-woken turns; 0 if not applicable
}

type line struct {
	Type             string `json:"type"`
	Timestamp        string `json:"timestamp"`
	AttributionSkill string `json:"attributionSkill"`
	AiTitle          string `json:"aiTitle"`
	CustomTitle      string `json:"customTitle"` // manual rename ({"type":"custom-title"}); beats ai-title
	Message          struct {
		ID      string          `json:"id"`
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   struct {
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

type entry struct {
	typ     string
	ts      int64
	content json.RawMessage
	model   string
	skill   string
	title   string
	custom  string
	msgID   string
	outTok  int64
}

// maxLineBytes caps a single transcript line (a large tool result / embedded
// image can be big). A longer line is skipped, not buffered whole.
const maxLineBytes = 32 * 1024 * 1024

// forEachLine calls fn for each newline-delimited line in r. Unlike a
// bufio.Scanner — whose Scan() stops for good on the first line over its buffer,
// silently dropping the title / tokens / start-id on every later line — an
// over-long line here is skipped and scanning continues. Memory stays bounded:
// such a line is discarded as it streams, never accumulated. fn must not retain
// its argument (it may point into a reused buffer).
func forEachLine(r io.Reader, max int, fn func([]byte)) {
	br := bufio.NewReaderSize(r, 256*1024)
	var line []byte // accumulates a line that spans multiple ReadSlice calls
	tooLong := false
	for {
		frag, err := br.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			if !tooLong {
				if len(line)+len(frag) > max {
					line, tooLong = nil, true // give up on this line, keep scanning
				} else {
					line = append(line, frag...)
				}
			}
			continue
		}
		if !tooLong {
			switch {
			case len(line) > 0:
				// Judge the CONTENT length (newline dropped), so a line of exactly
				// max bytes is kept whether it ends in '\n' or EOF — the old
				// len(line)+len(frag) counted the trailing '\n' and dropped a
				// newline-terminated line one byte under the cap.
				if l := dropNL(append(line, frag...)); len(l) <= max {
					fn(l)
				}
			case len(frag) > 0:
				fn(dropNL(frag))
			}
		}
		line, tooLong = line[:0], false
		if err != nil {
			return // EOF or read error
		}
	}
}

func dropNL(b []byte) []byte { return bytes.TrimRight(b, "\r\n") }

var taskIDRe = regexp.MustCompile(`<task-id>([^<]+)</task-id>`)

// Aggregate parses the transcript at path over the current turn and returns the
// residual values. The turn spans events at or after the supplied startEpoch
// (the prompt time, when the caller still has it) or, failing that, the
// corrected start recovered from the waking message's task id — which marks the
// task LAUNCH, later than the prompt, so it is a fallback, not an override.
func Aggregate(path string, startEpoch int64) Summary {
	entries := readEntries(path)
	corrected := correctStart(entries)
	effective := startEpoch
	if effective <= 0 {
		effective = corrected
	}

	sum := Summary{StartEpoch: corrected}
	perMsg := map[string]int64{}
	var customTitle string
	for _, e := range entries {
		if e.title != "" {
			sum.SessionTitle = e.title // session-wide: latest ai-title, not turn-filtered
		}
		if e.custom != "" {
			customTitle = e.custom // a manual rename; applied after the loop so it wins
		}
		// Turn-scoped fields (tokens/model/skill) need a known start to bound the
		// turn. With no known start (effective <= 0 — e.g. a task-woken turn whose
		// id we couldn't recover) we can't tell this turn's events from the rest of
		// the session, so we leave them unset rather than report whole-session
		// totals. SessionTitle above is session-wide, so it is still reported.
		if effective <= 0 || e.ts == 0 || e.ts < effective {
			continue
		}
		if e.typ == "assistant" {
			// One API message is split across one JSONL row per content block, each
			// carrying a copy of the same usage — summing every row reports ~2-3x
			// the real total. Count each message.id once, taking the largest value
			// seen so a row that happens to omit usage can't lock in a zero;
			// id-less rows are still counted individually.
			if e.msgID == "" {
				sum.OutputTokens += e.outTok
			} else if e.outTok > perMsg[e.msgID] {
				perMsg[e.msgID] = e.outTok
			}
			// "<synthetic>" is the placeholder model on locally-generated rows
			// (e.g. error stubs) — not something to show in a notification.
			if e.model != "" && e.model != "<synthetic>" {
				sum.Model = strings.TrimPrefix(e.model, "claude-")
			}
		}
		if e.skill != "" {
			sum.Skill = e.skill
		}
	}
	for _, v := range perMsg {
		sum.OutputTokens += v
	}
	// A manual session rename wins over the ai-title, matching Claude Code's UI
	// (and the "manual beats ai" precedence elsewhere): ai-title rows keep being
	// appended after a rename, so without this a renamed session shows a stale
	// auto-title in the fallback path.
	if customTitle != "" {
		sum.SessionTitle = customTitle
	}
	// Subagent / workflow output tokens live in a sibling transcript tree, not the
	// main file — fold them in so a Task/Workflow-heavy turn reports what it
	// actually generated, not just the orchestrator's share.
	sum.OutputTokens += subagentTokens(path, effective)
	return sum
}

// subagentTokens sums output tokens from this session's subagent / workflow
// transcripts. Claude Code writes those to a sibling
// <session-id>/subagents/**/agent-*.jsonl tree rather than inlining them into the
// main transcript, so Aggregate (which reads only the main file) would otherwise
// report a small fraction of a parallel turn's real output. Turn-scoped
// (ts >= effective) with the same per-message.id de-duplication; best-effort, so
// any walk / open / parse error is ignored. Returns 0 when the start is unknown
// (effective <= 0): without a turn boundary we can't tell this turn's subagent
// rows from the rest of the session.
//
// The per-message map is keyed by FILE + message.id, not id alone: each
// agent-*.jsonl is an independent transcript, so the same id appearing in two
// of them is two distinct messages — a global id key would max-collapse them and
// undercount. Within one file the content-block rows still share an id and are
// de-duplicated to the max (the usual 2-3x split-row problem).
func subagentTokens(mainPath string, effective int64) int64 {
	if effective <= 0 {
		return 0
	}
	root := filepath.Join(strings.TrimSuffix(mainPath, ".jsonl"), "subagents")
	perMsg := map[string]int64{}
	var idless int64
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() ||
			!strings.HasPrefix(d.Name(), "agent-") || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		f, ferr := os.Open(p)
		if ferr != nil {
			return nil
		}
		defer f.Close()
		forEachLine(f, maxLineBytes, func(b []byte) {
			var l line
			if json.Unmarshal(b, &l) != nil || l.Type != "assistant" {
				return
			}
			if ts := epoch(l.Timestamp); ts == 0 || ts < effective {
				return
			}
			if l.Message.ID == "" {
				idless += l.Message.Usage.OutputTokens
			} else if key := p + "\x00" + l.Message.ID; l.Message.Usage.OutputTokens > perMsg[key] {
				perMsg[key] = l.Message.Usage.OutputTokens
			}
		})
		return nil
	})
	total := idless
	for _, v := range perMsg {
		total += v
	}
	return total
}

// LatestTitle returns the most recent ai-title in the transcript, or "" if none.
// Used by mid-turn hooks (Notification, PreToolUse) that don't aggregate a turn
// but still want the session name (hook stdin's session_title is unreliable).
// It decodes only the title fields, so it avoids building the full entry slice.
// A manual rename (custom-title) wins over the auto ai-title.
func LatestTitle(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	var ai, custom string
	forEachLine(f, maxLineBytes, func(b []byte) {
		var l struct {
			AiTitle     string `json:"aiTitle"`
			CustomTitle string `json:"customTitle"`
		}
		if json.Unmarshal(b, &l) != nil {
			return
		}
		if l.AiTitle != "" {
			ai = l.AiTitle
		}
		if l.CustomTitle != "" {
			custom = l.CustomTitle
		}
	})
	// A manual rename wins over the auto ai-title (matches Claude Code's UI).
	if custom != "" {
		return custom
	}
	return ai
}

func readEntries(path string) []entry {
	f, err := os.Open(path)
	if err != nil {
		// An unreadable transcript yields a zero Summary (no model/tokens/title)
		// and, if state is also missing, a withheld completion ping — with no
		// other trace, since this package is otherwise silent. Surface it under
		// AGENTDONE_DEBUG like the state/slack/config failure paths.
		if os.Getenv("AGENTDONE_DEBUG") != "" {
			fmt.Fprintf(os.Stderr, "agentdone: transcript unreadable at %s: %v\n", path, err)
		}
		return nil
	}
	defer f.Close()

	var out []entry
	forEachLine(f, maxLineBytes, func(b []byte) {
		var l line
		if json.Unmarshal(b, &l) != nil {
			return
		}
		// content is only needed on user rows (the wake message and the
		// tool_result that carries the launched task id — see correctStart).
		// Holding every assistant row's content would pin the whole session's
		// output (100MB+ on a long session) in the heap for nothing.
		content := l.Message.Content
		if l.Type != "user" {
			content = nil
		}
		out = append(out, entry{
			typ:     l.Type,
			ts:      epoch(l.Timestamp),
			content: content,
			model:   l.Message.Model,
			skill:   l.AttributionSkill,
			title:   l.AiTitle,
			custom:  l.CustomTitle,
			msgID:   l.Message.ID,
			outTok:  l.Message.Usage.OutputTokens,
		})
	})
	return out
}

// correctStart implements the start-epoch correction for turns woken by a
// background task: it finds the <task-id> in the waking user message and returns
// the earliest timestamp at which that id appears (≈ when the task launched).
//
// The launch id first appears in the content of the tool result that created
// the task — a USER row, like the wake message itself (readEntries keeps
// content only for user rows) — so we scan those entries instead of re-opening
// the transcript. It only matters for task-woken turns (when a wake id is
// found), so the common case pays nothing.
func correctStart(entries []entry) int64 {
	var woke string
	for _, e := range entries {
		if e.typ == "user" && isInput(e.content) {
			woke = contentText(e.content)
		}
	}
	m := taskIDRe.FindStringSubmatch(woke)
	if len(m) < 2 {
		return 0
	}
	tid := []byte(m[1])

	var earliest int64
	for _, e := range entries {
		if e.ts == 0 || !bytes.Contains(e.content, tid) {
			continue
		}
		if earliest == 0 || e.ts < earliest {
			earliest = e.ts
		}
	}
	return earliest
}

// isInput reports whether a user message is an actual prompt/wake (string or a
// content array without a tool_result), as opposed to a tool result.
func isInput(content json.RawMessage) bool {
	c := bytes.TrimSpace(content)
	if len(c) == 0 {
		return false
	}
	switch c[0] {
	case '"':
		return true
	case '[':
		// Parse the element types rather than byte-matching "type":"tool_result"
		// (which JSON whitespace would defeat): it's a real prompt/wake unless an
		// element is a tool_result delivery.
		var arr []struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(c, &arr) != nil {
			return false
		}
		for _, e := range arr {
			if e.Type == "tool_result" {
				return false
			}
		}
		return true
	}
	return false
}

func contentText(content json.RawMessage) string {
	c := bytes.TrimSpace(content)
	if len(c) == 0 {
		return ""
	}
	if c[0] == '"' {
		var s string
		_ = json.Unmarshal(c, &s)
		return s
	}
	if c[0] == '[' {
		var arr []struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(c, &arr)
		var parts []string
		for _, e := range arr {
			if e.Text != "" {
				parts = append(parts, e.Text)
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

// epoch returns the timestamp as Unix milliseconds. Sub-second precision (vs
// whole seconds) keeps a prior turn's assistant message that lands in the same
// second as this turn's start from leaking into the turn's token total.
func epoch(ts string) int64 {
	if ts == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}
