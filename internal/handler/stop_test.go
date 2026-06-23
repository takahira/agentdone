package handler

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/takahira/agentdone/internal/state"
	"github.com/takahira/agentdone/pkg/cchooks"
)

// TestStop exercises the Stop handler against a local webhook server (never the
// real Slack) and checks the core behaviour: withhold while background work
// runs, send otherwise.
func TestStop(t *testing.T) {
	bodies := make(chan string, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies <- string(b)
	}))
	defer srv.Close()
	t.Setenv("SLACK_WEBHOOK_URL", srv.URL)

	seed := func(sid string, start int64) {
		if err := state.Save(sid, state.Turn{StartEpoch: start, Prompt: "p", SessionTitle: "title"}); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("suppressed while background running", func(t *testing.T) {
		seed("s1", time.Now().UnixMilli()-600_000)
		Stop(&cchooks.Stop{
			Common:               cchooks.Common{HookEventName: cchooks.EventStop, SessionID: "s1"},
			LastAssistantMessage: "並列で走らせています。お待ちください。",
			BackgroundTasks:      []cchooks.BackgroundTask{{Status: "running"}},
		})
		if len(bodies) != 0 {
			t.Fatalf("expected no notification (suppressed), got %d", len(bodies))
		}
	})

	t.Run("sends completion when nothing is running", func(t *testing.T) {
		seed("s2", time.Now().UnixMilli()-600_000)
		Stop(&cchooks.Stop{
			Common:               cchooks.Common{HookEventName: cchooks.EventStop, SessionID: "s2"},
			LastAssistantMessage: "全部できました。",
		})
		select {
		case b := <-bodies:
			if !strings.Contains(b, "完了") {
				t.Fatalf("body missing 完了: %s", b)
			}
		case <-time.After(time.Second):
			t.Fatal("expected a notification, got none")
		}
	})

	t.Run("suppress then complete keeps prompt on same session", func(t *testing.T) {
		// The flagship path: a turn is withheld while background work runs, then the
		// task wakes a later turn that has no UserPromptSubmit of its own. The
		// withheld Stop must NOT consume the turn state, or the completion loses
		// プロンプト (this is the bug this test guards against).
		seed("s4", time.Now().UnixMilli()-600_000)

		// Stop #1: background still running -> withheld, state must survive.
		Stop(&cchooks.Stop{
			Common:               cchooks.Common{HookEventName: cchooks.EventStop, SessionID: "s4"},
			LastAssistantMessage: "並列で走らせています。",
			BackgroundTasks:      []cchooks.BackgroundTask{{Status: "running"}},
		})
		if len(bodies) != 0 {
			t.Fatalf("expected no notification on the withheld Stop, got %d", len(bodies))
		}

		// Stop #2: background drained (task-woken completion), no new UserPromptSubmit.
		Stop(&cchooks.Stop{
			Common:               cchooks.Common{HookEventName: cchooks.EventStop, SessionID: "s4"},
			LastAssistantMessage: "全部できました。",
		})
		select {
		case b := <-bodies:
			if !strings.Contains(b, "完了") {
				t.Fatalf("body missing 完了: %s", b)
			}
			if !strings.Contains(b, "プロンプト：p") {
				t.Fatalf("prompt lost after suppress->complete (state was consumed too early): %s", b)
			}
		case <-time.After(time.Second):
			t.Fatal("expected a completion notification, got none")
		}
	})

	t.Run("confirmation during background keeps state for the woken completion", func(t *testing.T) {
		// a confirmation question raised WHILE background work is still
		// running must surface the waiting ping without consuming the turn state.
		// The task later wakes a Stop with no UserPromptSubmit of its own, and that
		// completion still needs プロンプト / start time. The question path used to
		// Delete the state unconditionally, dropping the eventual completion.
		seed("s5", time.Now().UnixMilli()-600_000)

		// Stop #1: question + background running -> waiting ping, state must survive.
		Stop(&cchooks.Stop{
			Common:               cchooks.Common{HookEventName: cchooks.EventStop, SessionID: "s5"},
			LastAssistantMessage: "コミットして進めてよいですか？",
			BackgroundTasks:      []cchooks.BackgroundTask{{Status: "running"}},
		})
		select {
		case b := <-bodies:
			if !strings.Contains(b, "確認待ち") {
				t.Fatalf("Stop #1 should send the waiting ping: %s", b)
			}
		case <-time.After(time.Second):
			t.Fatal("expected the waiting ping on the question+background Stop, got none")
		}

		// Stop #2: background drained (task-woken completion), no new UserPromptSubmit.
		Stop(&cchooks.Stop{
			Common:               cchooks.Common{HookEventName: cchooks.EventStop, SessionID: "s5"},
			LastAssistantMessage: "全部できました。",
		})
		select {
		case b := <-bodies:
			if !strings.Contains(b, "完了") {
				t.Fatalf("Stop #2 should send the completion: %s", b)
			}
			if !strings.Contains(b, "プロンプト：p") {
				t.Fatalf("prompt lost: the question path consumed state too early: %s", b)
			}
		case <-time.After(time.Second):
			t.Fatal("expected a completion after the question+background turn, got none")
		}
	})

	t.Run("saved start wins over corrected start (short background task)", func(t *testing.T) {
		// the transcript correction points at the task LAUNCH, which is later
		// than the prompt. When the saved state survived, the correction must not
		// override it: a background task that finished in under the threshold
		// would make a 10-minute turn look "too short", and — the state already
		// consumed — the flagship completion notification would vanish entirely.
		now := time.Now()
		launch := now.Add(-2 * time.Minute).UTC().Format(time.RFC3339Nano)
		wake := now.Add(-time.Second).UTC().Format(time.RFC3339Nano)
		tr := filepath.Join(t.TempDir(), "t.jsonl")
		lines := fmt.Sprintf(
			`{"type":"user","timestamp":"%s","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"task bgtask42 started"}]}}`+"\n"+
				`{"type":"user","timestamp":"%s","message":{"content":"<task-id>bgtask42</task-id> finished"}}`+"\n",
			launch, wake)
		if err := os.WriteFile(tr, []byte(lines), 0o600); err != nil {
			t.Fatal(err)
		}
		seed("s6", now.UnixMilli()-600_000)
		Stop(&cchooks.Stop{
			Common:               cchooks.Common{HookEventName: cchooks.EventStop, SessionID: "s6", TranscriptPath: tr},
			LastAssistantMessage: "全部できました。",
		})
		select {
		case b := <-bodies:
			if !strings.Contains(b, "完了") {
				t.Fatalf("body missing 完了: %s", b)
			}
		case <-time.After(time.Second):
			t.Fatal("completion dropped: the corrected (launch) start overrode the saved prompt start")
		}
	})

	t.Run("AGENTDONE_THRESHOLD=0 reports a short completion", func(t *testing.T) {
		// the 300s floor is tunable; 0 means every completion notifies.
		t.Setenv("AGENTDONE_THRESHOLD", "0")
		seed("s7", time.Now().UnixMilli()-10_000) // 10s turn, far under the default floor
		Stop(&cchooks.Stop{
			Common:               cchooks.Common{HookEventName: cchooks.EventStop, SessionID: "s7"},
			LastAssistantMessage: "全部できました。",
		})
		select {
		case b := <-bodies:
			if !strings.Contains(b, "完了") {
				t.Fatalf("body missing 完了: %s", b)
			}
		case <-time.After(time.Second):
			t.Fatal("expected a notification with threshold 0, got none")
		}
	})

	t.Run("AGENTDONE_THRESHOLD=0 reports a completion with no known start", func(t *testing.T) {
		// "0 reports every completion" includes a turn whose start is unknown (no
		// state, no transcript correction): the unknown-start withhold exists only
		// to apply the time floor, and with the floor gone it must not silence the
		// turn. The duration is simply omitted.
		t.Setenv("AGENTDONE_THRESHOLD", "0")
		// no seed: s8 has no saved state and no transcript to correct from
		Stop(&cchooks.Stop{
			Common:               cchooks.Common{HookEventName: cchooks.EventStop, SessionID: "s8"},
			LastAssistantMessage: "全部できました。",
		})
		select {
		case b := <-bodies:
			if !strings.Contains(b, "完了") {
				t.Fatalf("body missing 完了: %s", b)
			}
		case <-time.After(time.Second):
			t.Fatal("expected a notification with threshold 0 and unknown start, got none")
		}
		// With a non-zero threshold the same turn stays withheld (the documented
		// trade-off against spamming short turns).
		t.Setenv("AGENTDONE_THRESHOLD", "300")
		Stop(&cchooks.Stop{
			Common:               cchooks.Common{HookEventName: cchooks.EventStop, SessionID: "s8"},
			LastAssistantMessage: "全部できました。",
		})
		if len(bodies) != 0 {
			t.Fatalf("expected no notification for unknown start with a non-zero threshold, got %d", len(bodies))
		}
	})

	t.Run("confirmation question overrides threshold and suppression", func(t *testing.T) {
		// Recent start (under threshold) AND background running: a question must
		// still surface, overriding both the time threshold and suppression.
		seed("s3", time.Now().UnixMilli())
		Stop(&cchooks.Stop{
			Common:               cchooks.Common{HookEventName: cchooks.EventStop, SessionID: "s3"},
			LastAssistantMessage: "コミットして進めてよいですか？",
			BackgroundTasks:      []cchooks.BackgroundTask{{Status: "running"}},
		})
		select {
		case b := <-bodies:
			if !strings.Contains(b, "確認待ち") {
				t.Fatalf("body missing 確認待ち: %s", b)
			}
		case <-time.After(time.Second):
			t.Fatal("expected a confirmation notification, got none")
		}
	})
}

// If the wall clock ran backwards since the turn started (NTP step, manual
// change), elapsed goes negative. A non-ask completion must then be treated as
// start-unknown (withheld under a non-zero threshold) rather than fire/withhold
// on a meaningless negative comparison — and AGENTDONE_THRESHOLD=0 still sends.
func TestStopClockRewind(t *testing.T) {
	bodies := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies <- string(b)
	}))
	defer srv.Close()
	t.Setenv("SLACK_WEBHOOK_URL", srv.URL)

	future := time.Now().UnixMilli() + 600_000 // start 10 min in the "future"
	if err := state.Save("rw", state.Turn{StartEpoch: future, Prompt: "p", SessionTitle: "t"}); err != nil {
		t.Fatal(err)
	}
	Stop(&cchooks.Stop{
		Common:               cchooks.Common{HookEventName: cchooks.EventStop, SessionID: "rw"},
		LastAssistantMessage: "全部できました。",
	})
	if len(bodies) != 0 {
		t.Fatalf("negative elapsed should be treated as unknown start (withheld), got %d", len(bodies))
	}

	t.Setenv("AGENTDONE_THRESHOLD", "0")
	if err := state.Save("rw0", state.Turn{StartEpoch: future, Prompt: "p", SessionTitle: "t"}); err != nil {
		t.Fatal(err)
	}
	Stop(&cchooks.Stop{
		Common:               cchooks.Common{HookEventName: cchooks.EventStop, SessionID: "rw0"},
		LastAssistantMessage: "全部できました。",
	})
	select {
	case b := <-bodies:
		if !strings.Contains(b, "完了") {
			t.Fatalf("threshold=0 should notify even with a bad clock: %s", b)
		}
	case <-time.After(time.Second):
		t.Fatal("threshold=0 should send a completion, got none")
	}
}

// An invalid AGENTDONE_THRESHOLD ("5m", a negative, garbage) falls back to the
// default rather than erroring or being mis-parsed; "0" and a valid integer are
// honoured.
func TestThresholdSeconds_Invalid(t *testing.T) {
	for _, v := range []string{"5m", "30m", "-5", "abc", "9999999999999999999999"} {
		t.Setenv("AGENTDONE_THRESHOLD", v)
		if got := thresholdSeconds(); got != defaultThresholdSeconds {
			t.Errorf("thresholdSeconds() with %q = %d, want default %d", v, got, defaultThresholdSeconds)
		}
	}
	t.Setenv("AGENTDONE_THRESHOLD", "0")
	if got := thresholdSeconds(); got != 0 {
		t.Errorf("thresholdSeconds() with 0 = %d, want 0", got)
	}
	t.Setenv("AGENTDONE_THRESHOLD", "120")
	if got := thresholdSeconds(); got != 120 {
		t.Errorf("thresholdSeconds() with 120 = %d, want 120", got)
	}
}
