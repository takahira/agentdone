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

// English is the shipped default, but the rest of the handler suite forces
// Japanese (main_test.go), so exercise the real dispatch path in English here.
func TestStopEnglishIntegration(t *testing.T) {
	t.Setenv("AGENTDONE_LANG", "en")
	bodies := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies <- string(b)
	}))
	defer srv.Close()
	t.Setenv("SLACK_WEBHOOK_URL", srv.URL)

	// A completed turn renders the English labels.
	if err := state.Save("en1", state.Turn{StartEpoch: time.Now().UnixMilli() - 600_000, Prompt: "run the tests", SessionTitle: "Refactor"}); err != nil {
		t.Fatal(err)
	}
	defer state.Delete("en1")
	Stop(&cchooks.Stop{
		Common:               cchooks.Common{HookEventName: cchooks.EventStop, SessionID: "en1"},
		LastAssistantMessage: "Ran the tests and reported back.",
	})
	select {
	case b := <-bodies:
		for _, want := range []string{"Done", "Session: Refactor", "Prompt: run the tests", "Did: Ran the tests"} {
			if !strings.Contains(b, want) {
				t.Errorf("English completion missing %q in:\n%s", want, b)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("expected an English completion notification, got none")
	}

	// A plain-text English confirmation question (no '?', front-loaded phrase)
	// must still surface through the real Stop path.
	if err := state.Save("en2", state.Turn{StartEpoch: time.Now().UnixMilli(), Prompt: "p", SessionTitle: "T"}); err != nil {
		t.Fatal(err)
	}
	defer state.Delete("en2")
	Stop(&cchooks.Stop{
		Common:               cchooks.Common{HookEventName: cchooks.EventStop, SessionID: "en2"},
		LastAssistantMessage: "I finished the first pass. Should I also update the docs now",
	})
	select {
	case b := <-bodies:
		if !strings.Contains(b, "Waiting for confirmation") {
			t.Errorf("English question not surfaced as 'Waiting for confirmation':\n%s", b)
		}
	case <-time.After(time.Second):
		t.Fatal("expected an English confirmation notification, got none")
	}
}

func TestNotificationEnglishIntegration(t *testing.T) {
	t.Setenv("AGENTDONE_LANG", "en")
	bodies := make(chan string, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies <- string(b)
	}))
	defer srv.Close()
	t.Setenv("SLACK_WEBHOOK_URL", srv.URL)

	Notification(&cchooks.Notification{
		Common:           cchooks.Common{HookEventName: cchooks.EventNotification, SessionID: "enN"},
		NotificationType: "permission_prompt",
		Message:          "Claude needs permission to run a command",
	})
	select {
	case b := <-bodies:
		if !strings.Contains(b, "Waiting for permission") {
			t.Errorf("English permission notification wrong:\n%s", b)
		}
	case <-time.After(time.Second):
		t.Fatal("expected an English permission notification, got none")
	}
}
