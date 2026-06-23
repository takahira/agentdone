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
	if _, ok := state.Peek("f1"); ok {
		t.Error("turn state was not consumed after StopFailure")
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
