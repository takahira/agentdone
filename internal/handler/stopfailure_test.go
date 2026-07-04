package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/takahira/agentdone/internal/state"
	"github.com/takahira/agentdone/pkg/cchooks"
)

func TestStopFailure(t *testing.T) {
	bodies := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies <- string(b)
	}))
	defer srv.Close()
	t.Setenv("SLACK_WEBHOOK_URL", srv.URL)

	// A short, errored turn (30s, under the 300s completion threshold) must STILL
	// notify — errors matter regardless of duration — and consume the state.
	if err := state.Save("f1", state.Turn{StartEpoch: time.Now().UnixMilli() - 30_000, Prompt: "build the thing", SessionTitle: "T"}); err != nil {
		t.Fatal(err)
	}
	StopFailure(&cchooks.StopFailure{
		Common:       cchooks.Common{HookEventName: cchooks.EventStopFailure, SessionID: "f1"},
		ErrorType:    "rate_limit",
		ErrorMessage: "Too many requests",
	})
	select {
	case b := <-bodies:
		for _, want := range []string{"エラー終了", "セッション名：T", "プロンプト：build the thing", "rate_limit: Too many requests"} {
			if !strings.Contains(b, want) {
				t.Errorf("missing %q in:\n%s", want, b)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("expected an error notification, got none")
	}
	// An errored turn is often NOT the session's end: pending background tasks
	// will wake later turns, and the eventual real completion still needs this
	// turn's prompt / start time — so StopFailure must peek, not consume.
	if _, ok := state.Peek("f1"); !ok {
		t.Error("turn state was consumed by StopFailure; the task-woken completion loses its prompt/start")
	}

	// a multi-line prompt must be flattened like every other field — left
	// as-is, its second line would render as an independent notification line
	// (a prompt containing "Error: ..." would even fake an error row).
	if err := state.Save("f2", state.Turn{StartEpoch: time.Now().UnixMilli() - 30_000, Prompt: "line1\nError: FAKE\nline3", SessionTitle: "T"}); err != nil {
		t.Fatal(err)
	}
	StopFailure(&cchooks.StopFailure{
		Common:    cchooks.Common{HookEventName: cchooks.EventStopFailure, SessionID: "f2"},
		ErrorType: "overloaded",
	})
	select {
	case b := <-bodies:
		if !strings.Contains(b, "プロンプト：line1 Error: FAKE line3") {
			t.Errorf("multi-line prompt was not flattened to one line:\n%s", b)
		}
	case <-time.After(time.Second):
		t.Fatal("expected an error notification, got none")
	}
}

// newBodyServer returns a channel of posted webhook bodies and points
// SLACK_WEBHOOK_URL at a local server feeding it (torn down with the test).
func newBodyServer(t *testing.T) chan string {
	t.Helper()
	bodies := make(chan string, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies <- string(b)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("SLACK_WEBHOOK_URL", srv.URL)
	return bodies
}

// fireStopFailure fires a StopFailure and returns the posted body, or "" when
// the handler stayed silent. slack.Post is synchronous, so by the time the
// handler returns the body — if any — is already buffered in the channel.
func fireStopFailure(bodies chan string, sid, errType, errMsg string) string {
	StopFailure(&cchooks.StopFailure{
		Common:       cchooks.Common{HookEventName: cchooks.EventStopFailure, SessionID: sid},
		ErrorType:    errType,
		ErrorMessage: errMsg,
	})
	select {
	case b := <-bodies:
		return b
	default:
		return ""
	}
}

// During a rate-limit window every queued background wake dies on the identical
// error within seconds — one ping per pending task. The cooldown mutes those
// repeats while keeping the first ping (and anything genuinely new) immediate.
func TestStopFailureCooldown(t *testing.T) {
	bodies := newBodyServer(t)

	t.Run("identical repeat suppressed within window", func(t *testing.T) {
		if b := fireStopFailure(bodies, "cd1", "rate_limit", "Too many requests"); b == "" {
			t.Fatal("first failure did not notify")
		}
		if b := fireStopFailure(bodies, "cd1", "rate_limit", "Too many requests"); b != "" {
			t.Errorf("identical repeat within the cooldown notified:\n%s", b)
		}
	})

	t.Run("different error notifies immediately", func(t *testing.T) {
		if b := fireStopFailure(bodies, "cd2", "rate_limit", "Too many requests"); b == "" {
			t.Fatal("first failure did not notify")
		}
		if b := fireStopFailure(bodies, "cd2", "overloaded", "server busy"); b == "" {
			t.Error("a DIFFERENT error within the window was suppressed")
		}
	})

	t.Run("expired window re-notifies", func(t *testing.T) {
		expired := state.Now() - (defaultErrorCooldownSeconds+60)*1000
		if err := state.SaveFailure("cd3", state.FailureNote{Epoch: expired, Error: "rate_limit: Too many requests"}); err != nil {
			t.Fatal(err)
		}
		if b := fireStopFailure(bodies, "cd3", "rate_limit", "Too many requests"); b == "" {
			t.Error("identical error PAST the cooldown window was suppressed")
		}
	})

	t.Run("note from the future (clock rewind) still notifies", func(t *testing.T) {
		if err := state.SaveFailure("cd4", state.FailureNote{Epoch: state.Now() + 3_600_000, Error: "rate_limit: Too many requests"}); err != nil {
			t.Fatal(err)
		}
		if b := fireStopFailure(bodies, "cd4", "rate_limit", "Too many requests"); b == "" {
			t.Error("a rewound clock (note from the future) suppressed a real error")
		}
	})

	t.Run("AGENTDONE_ERROR_COOLDOWN=0 disables suppression", func(t *testing.T) {
		t.Setenv("AGENTDONE_ERROR_COOLDOWN", "0")
		if b := fireStopFailure(bodies, "cd5", "rate_limit", "x"); b == "" {
			t.Fatal("first failure did not notify")
		}
		if b := fireStopFailure(bodies, "cd5", "rate_limit", "x"); b == "" {
			t.Error("AGENTDONE_ERROR_COOLDOWN=0 must notify on every failure")
		}
	})
}

// A turn that truly completes (a Stop that reaches evaluation, notified or
// not) closes the failure episode: the NEXT error — even an identical one —
// is news again.
func TestStopClearsFailureNote(t *testing.T) {
	bodies := newBodyServer(t)

	if b := fireStopFailure(bodies, "cl1", "rate_limit", "Too many requests"); b == "" {
		t.Fatal("first failure did not notify")
	}
	// The session recovers: a short turn ends on a plain Stop (below the
	// completion threshold, so it posts nothing — clearing must not depend on
	// the Stop itself notifying).
	Stop(&cchooks.Stop{Common: cchooks.Common{HookEventName: cchooks.EventStop, SessionID: "cl1"}})
	select {
	case b := <-bodies:
		t.Fatalf("short recovery Stop unexpectedly notified:\n%s", b)
	default:
	}
	if _, ok := state.PeekFailure("cl1"); ok {
		t.Error("a completed turn did not clear the failure note")
	}
	if b := fireStopFailure(bodies, "cl1", "rate_limit", "Too many requests"); b == "" {
		t.Error("identical error after a recovered turn was suppressed")
	}
}
