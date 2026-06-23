package transcript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAggregateSubSecondBoundary(t *testing.T) {
	// a prior turn's assistant message sharing the same whole second as
	// this turn's start (but an earlier millisecond) must NOT leak into the turn's
	// token total — millisecond precision keeps them apart. (Whole-second epochs
	// would count the .100 message because it rounds to the same second as .500.)
	p := write(t,
		`{"type":"assistant","timestamp":"2026-06-07T08:00:00.100Z","message":{"usage":{"output_tokens":1000}}}`,
		`{"type":"assistant","timestamp":"2026-06-07T08:00:01.000Z","message":{"usage":{"output_tokens":7}}}`,
	)
	if got := Aggregate(p, ep("2026-06-07T08:00:00.500Z")).OutputTokens; got != 7 {
		t.Errorf("OutputTokens = %d, want 7 (the .100 message precedes the .500 start and must not leak)", got)
	}
}

func TestIsInput(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"string prompt", `"just text"`, true},
		{"text array", `[{"type":"text","text":"hi"}]`, true},
		{"tool_result compact", `[{"type":"tool_result","tool_use_id":"x"}]`, false},
		{"tool_result with spaces", `[{"type" : "tool_result" , "tool_use_id":"x"}]`, false}, // a byte-substring check would miss this
		{"empty", ``, false},
	}
	for _, c := range cases {
		if got := isInput(json.RawMessage(c.content)); got != c.want {
			t.Errorf("%s: isInput(%s) = %v, want %v", c.name, c.content, got, c.want)
		}
	}
}

func TestForEachLineNormal(t *testing.T) {
	r := strings.NewReader("one\ntwo\nthree") // final line has no trailing newline
	var got []string
	forEachLine(r, 1024, func(b []byte) { got = append(got, string(b)) })
	if strings.Join(got, ",") != "one,two,three" {
		t.Errorf("forEachLine got %q, want [one two three]", got)
	}
}

func TestForEachLineSkipsHugeLine(t *testing.T) {
	// a single line over the cap (here larger than the internal read
	// buffer, so it streams as several fragments) must be skipped WITHOUT
	// aborting the scan — the lines after it still get read. A bufio.Scanner
	// would stop for good and drop "xy".
	huge := strings.Repeat("Z", 300*1024)
	r := strings.NewReader("abc\n" + huge + "\nxy\n")
	var got []string
	forEachLine(r, 8, func(b []byte) { got = append(got, string(b)) })
	if strings.Join(got, ",") != "abc,xy" {
		t.Errorf("forEachLine got %q, want [abc xy] (over-long line skipped, scan continues)", got)
	}
}

func write(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(p, []byte(joinLines(lines)), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func joinLines(lines []string) string {
	s := ""
	for _, l := range lines {
		s += l + "\n"
	}
	return s
}

func ep(iso string) int64 {
	tm, _ := time.Parse(time.RFC3339Nano, iso)
	return tm.UnixMilli()
}

func TestAggregateTokensModelSkill(t *testing.T) {
	p := write(t,
		`{"type":"ai-title","aiTitle":"Review Slack webhook setup"}`,
		`{"type":"assistant","timestamp":"2026-06-07T08:00:05.000Z","message":{"model":"claude-opus-4-8","usage":{"output_tokens":1200}}}`,
		`{"type":"assistant","timestamp":"2026-06-07T08:00:15.000Z","attributionSkill":"code-review","message":{"model":"claude-opus-4-8","usage":{"output_tokens":300}}}`,
	)
	got := Aggregate(p, ep("2026-06-07T08:00:00.000Z"))
	if got.OutputTokens != 1500 {
		t.Errorf("OutputTokens = %d, want 1500", got.OutputTokens)
	}
	if got.Model != "opus-4-8" {
		t.Errorf("Model = %q, want opus-4-8", got.Model)
	}
	if got.Skill != "code-review" {
		t.Errorf("Skill = %q, want code-review", got.Skill)
	}
	if got.SessionTitle != "Review Slack webhook setup" {
		t.Errorf("SessionTitle = %q, want Review Slack webhook setup", got.SessionTitle)
	}
	if got.StartEpoch != 0 {
		t.Errorf("StartEpoch = %d, want 0 (no correction)", got.StartEpoch)
	}
}

func TestAggregateNoStartSkipsTurnScopedFields(t *testing.T) {
	// with no known start (startEpoch 0 and a wake message carrying no
	// recoverable <task-id>, so correctStart returns 0), effective is 0 and we
	// cannot bound the turn. Turn-scoped fields must stay unset rather than sum
	// across the whole session; SessionTitle (session-wide) is still reported.
	p := write(t,
		`{"type":"ai-title","aiTitle":"Some session"}`,
		`{"type":"assistant","timestamp":"2026-06-07T08:00:05.000Z","message":{"model":"claude-opus-4-8","usage":{"output_tokens":50000}}}`,
		`{"type":"user","timestamp":"2026-06-07T08:30:00.000Z","message":{"content":"woke with no task id"}}`,
		`{"type":"assistant","timestamp":"2026-06-07T08:30:05.000Z","attributionSkill":"code-review","message":{"model":"claude-haiku-4-5","usage":{"output_tokens":12}}}`,
	)
	got := Aggregate(p, 0)
	if got.OutputTokens != 0 {
		t.Errorf("OutputTokens = %d, want 0 (unknown start must skip turn-scoped sum, not report whole-session 50012)", got.OutputTokens)
	}
	if got.Model != "" || got.Skill != "" {
		t.Errorf("Model/Skill = %q/%q, want both empty when the start is unknown", got.Model, got.Skill)
	}
	if got.SessionTitle != "Some session" {
		t.Errorf("SessionTitle = %q, want \"Some session\" (session-wide, always reported)", got.SessionTitle)
	}
}

func TestAggregateDeduplicatesUsageByMessageID(t *testing.T) {
	// one API message is written as one JSONL row per content block, and
	// every row repeats the same usage. Summing them all reported ~2-3x the real
	// output tokens; the total must count each message.id once — by largest
	// value, so a row of the message that omits usage (msg_b's first row) can't
	// lock in a zero. Rows with no id are still counted individually.
	p := write(t,
		`{"type":"assistant","timestamp":"2026-06-07T08:00:05.000Z","message":{"id":"msg_a","usage":{"output_tokens":1000}}}`,
		`{"type":"assistant","timestamp":"2026-06-07T08:00:06.000Z","message":{"id":"msg_a","usage":{"output_tokens":1000}}}`,
		`{"type":"assistant","timestamp":"2026-06-07T08:00:07.000Z","message":{"id":"msg_a","usage":{"output_tokens":1000}}}`,
		`{"type":"assistant","timestamp":"2026-06-07T08:00:09.000Z","message":{"id":"msg_b"}}`,
		`{"type":"assistant","timestamp":"2026-06-07T08:00:10.000Z","message":{"id":"msg_b","usage":{"output_tokens":200}}}`,
		`{"type":"assistant","timestamp":"2026-06-07T08:00:12.000Z","message":{"usage":{"output_tokens":5}}}`,
	)
	if got := Aggregate(p, ep("2026-06-07T08:00:00.000Z")).OutputTokens; got != 1205 {
		t.Errorf("OutputTokens = %d, want 1205 (msg_a counted once, msg_b's empty row ignored)", got)
	}
}

func TestAggregateSkipsSyntheticModel(t *testing.T) {
	// "<synthetic>" is a local placeholder row, not a model to display.
	p := write(t,
		`{"type":"assistant","timestamp":"2026-06-07T08:00:05.000Z","message":{"model":"claude-opus-4-8","usage":{"output_tokens":10}}}`,
		`{"type":"assistant","timestamp":"2026-06-07T08:00:06.000Z","message":{"model":"<synthetic>","usage":{"output_tokens":1}}}`,
	)
	if got := Aggregate(p, ep("2026-06-07T08:00:00.000Z")).Model; got != "opus-4-8" {
		t.Errorf("Model = %q, want opus-4-8 (<synthetic> must not win)", got)
	}
}

func TestLatestTitle(t *testing.T) {
	p := write(t,
		`{"type":"ai-title","aiTitle":"First title"}`,
		`{"type":"assistant","timestamp":"2026-06-07T08:00:05.000Z","message":{"model":"claude-opus-4-8"}}`,
		`{"type":"ai-title","aiTitle":"Renamed title"}`,
	)
	if got := LatestTitle(p); got != "Renamed title" {
		t.Errorf("LatestTitle = %q, want %q (latest, not first)", got, "Renamed title")
	}
	if got := LatestTitle(filepath.Join(t.TempDir(), "missing.jsonl")); got != "" {
		t.Errorf("LatestTitle(missing) = %q, want \"\"", got)
	}
}

func TestAggregateStartCorrection(t *testing.T) {
	// Task "abc123" first appears at 08:00:05 (launch). Later a task-notification
	// user message wakes the turn at 08:30:00. The corrected start must point to
	// the launch, not the wake.
	p := write(t,
		`{"type":"assistant","timestamp":"2026-06-07T08:00:05.000Z","message":{"content":[{"type":"tool_use","id":"toolu_x"}]}}`,
		`{"type":"user","timestamp":"2026-06-07T08:00:06.000Z","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_x","content":"task abc123 started"}]}}`,
		`{"type":"user","timestamp":"2026-06-07T08:30:00.000Z","message":{"content":"<task-notification>\n<task-id>abc123</task-id>\n</task-notification>"}}`,
		`{"type":"assistant","timestamp":"2026-06-07T08:30:05.000Z","message":{"model":"claude-opus-4-8","usage":{"output_tokens":50}}}`,
	)
	got := Aggregate(p, ep("2026-06-07T08:30:00.000Z"))
	// The id "abc123" first appears in the 08:00:06 tool_result (≈ launch), which
	// is what the correction must point to — not the 08:30 wake.
	want := ep("2026-06-07T08:00:06.000Z")
	if got.StartEpoch != want {
		t.Errorf("StartEpoch = %d, want %d (task launch, not wake)", got.StartEpoch, want)
	}
}

// A manual rename (custom-title) wins over an ai-title even when ai-title rows
// keep being appended after the rename.
func TestAggregateCustomTitleWins(t *testing.T) {
	p := write(t,
		`{"type":"ai-title","aiTitle":"Old auto title"}`,
		`{"type":"custom-title","customTitle":"手動タイトル"}`,
		`{"type":"ai-title","aiTitle":"Newer auto title"}`,
		`{"type":"assistant","timestamp":"2026-06-07T08:00:05.000Z","message":{"usage":{"output_tokens":10}}}`,
	)
	if got := Aggregate(p, ep("2026-06-07T08:00:00.000Z")).SessionTitle; got != "手動タイトル" {
		t.Errorf("SessionTitle = %q, want 手動タイトル", got)
	}
	if got := LatestTitle(p); got != "手動タイトル" {
		t.Errorf("LatestTitle = %q, want 手動タイトル", got)
	}
}

// Subagent / workflow output tokens (written to a sibling
// <session-id>/subagents/**/agent-*.jsonl tree) are folded into the turn total.
func TestAggregateSubagentTokens(t *testing.T) {
	p := write(t,
		`{"type":"assistant","timestamp":"2026-06-07T08:00:05.000Z","message":{"id":"m-main","usage":{"output_tokens":100}}}`,
	)
	subDir := filepath.Join(strings.TrimSuffix(p, ".jsonl"), "subagents", "workflows", "wf1")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Two content rows of ONE message.id (deduped to the max), plus a second
	// message; an out-of-window row before the turn start must be ignored.
	agent := strings.Join([]string{
		`{"type":"assistant","timestamp":"2026-06-07T08:00:06.000Z","message":{"id":"a1","usage":{"output_tokens":500}}}`,
		`{"type":"assistant","timestamp":"2026-06-07T08:00:06.000Z","message":{"id":"a1","usage":{"output_tokens":2000}}}`,
		`{"type":"assistant","timestamp":"2026-06-07T08:00:07.000Z","message":{"id":"a2","usage":{"output_tokens":300}}}`,
		`{"type":"assistant","timestamp":"2026-06-07T07:59:00.000Z","message":{"id":"a3","usage":{"output_tokens":9999}}}`,
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(subDir, "agent-abc.jsonl"), []byte(agent), 0o600); err != nil {
		t.Fatal(err)
	}
	// main 100 + a1 max(500,2000)=2000 + a2 300 = 2400; a3 (pre-start) excluded.
	if got := Aggregate(p, ep("2026-06-07T08:00:00.000Z")).OutputTokens; got != 2400 {
		t.Errorf("OutputTokens = %d, want 2400 (100 main + 2000 + 300 subagent)", got)
	}
}

// The same message.id appearing in two different agent-*.jsonl files is two
// distinct messages and must both count — keying the dedup map by file+id avoids
// the global-id max-collapse that would undercount. idless rows across files sum.
func TestSubagentTokensPerFileIDKeying(t *testing.T) {
	p := write(t,
		`{"type":"assistant","timestamp":"2026-06-07T08:00:05.000Z","message":{"id":"m","usage":{"output_tokens":50}}}`,
	)
	base := strings.TrimSuffix(p, ".jsonl")
	for _, a := range []struct {
		dir, name, body string
	}{
		{filepath.Join(base, "subagents", "a"), "agent-1.jsonl",
			`{"type":"assistant","timestamp":"2026-06-07T08:00:06.000Z","message":{"id":"dup","usage":{"output_tokens":100}}}` + "\n" +
				`{"type":"assistant","timestamp":"2026-06-07T08:00:06.000Z","message":{"usage":{"output_tokens":7}}}`},
		{filepath.Join(base, "subagents", "b"), "agent-2.jsonl",
			`{"type":"assistant","timestamp":"2026-06-07T08:00:07.000Z","message":{"id":"dup","usage":{"output_tokens":200}}}` + "\n" +
				`{"type":"assistant","timestamp":"2026-06-07T08:00:07.000Z","message":{"usage":{"output_tokens":3}}}`},
	} {
		if err := os.MkdirAll(a.dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(a.dir, a.name), []byte(a.body+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// main 50 + file-a id"dup" 100 + file-b id"dup" 200 (NOT max-collapsed to 200)
	// + idless 7 + idless 3 = 360.
	if got := Aggregate(p, ep("2026-06-07T08:00:00.000Z")).OutputTokens; got != 360 {
		t.Errorf("OutputTokens = %d, want 360 (same id in two files must both count, + idless)", got)
	}
}

// A line of exactly maxLineBytes of content is kept whether it ends in '\n' or
// EOF — the old length check counted the trailing newline and dropped the
// newline-terminated one a byte under the cap.
func TestForEachLineExactMaxBoundary(t *testing.T) {
	const max = 600 * 1024
	body := strings.Repeat("Z", max) // exactly max content bytes
	for _, tc := range []struct {
		name string
		in   string
	}{
		{"newline-terminated", body + "\n"},
		{"eof-terminated", body},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			forEachLine(strings.NewReader(tc.in), max, func(b []byte) { got = append(got, string(b)) })
			if len(got) != 1 || len(got[0]) != max {
				n := 0
				if len(got) > 0 {
					n = len(got[0])
				}
				t.Errorf("emitted %d line(s) of %d bytes, want 1 line of %d", len(got), n, max)
			}
		})
	}
	// One byte over the cap is still dropped.
	over := strings.Repeat("Z", max+1) + "\n"
	var got []string
	forEachLine(strings.NewReader(over), max, func(b []byte) { got = append(got, string(b)) })
	if len(got) != 0 {
		t.Errorf("a line over the cap was emitted (%d lines)", len(got))
	}
}
