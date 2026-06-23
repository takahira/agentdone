package handler

import (
	"strings"
	"testing"

	"github.com/takahira/agentdone/internal/state"
	"github.com/takahira/agentdone/internal/transcript"
	"github.com/takahira/agentdone/pkg/cchooks"
)

func TestActiveMessages(t *testing.T) {
	// Clear every locale source so the cases below are deterministic regardless
	// of the host environment.
	t.Setenv("AGENTDONE_LANG", "")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "")
	if got := activeMessages().lang; got != "en" {
		t.Errorf("default lang = %q, want en", got)
	}
	t.Setenv("AGENTDONE_LANG", "ja_JP")
	if got := activeMessages().lang; got != "ja" {
		t.Errorf("AGENTDONE_LANG=ja_JP -> %q, want ja", got)
	}
	t.Setenv("AGENTDONE_LANG", "en")
	t.Setenv("LANG", "ja_JP.UTF-8")
	if got := activeMessages().lang; got != "en" {
		t.Errorf("AGENTDONE_LANG=en must override LANG, got %q", got)
	}
	t.Setenv("AGENTDONE_LANG", "")
	t.Setenv("LANG", "ja_JP.UTF-8")
	if got := activeMessages().lang; got != "ja" {
		t.Errorf("LANG=ja_JP -> %q, want ja", got)
	}
	// LC_ALL overrides LC_MESSAGES overrides LANG (POSIX).
	t.Setenv("LANG", "en_US.UTF-8")
	t.Setenv("LC_MESSAGES", "ja_JP.UTF-8")
	if got := activeMessages().lang; got != "ja" {
		t.Errorf("LC_MESSAGES=ja must override LANG=en, got %q", got)
	}
	t.Setenv("LC_ALL", "en_US.UTF-8")
	if got := activeMessages().lang; got != "en" {
		t.Errorf("LC_ALL=en must override LC_MESSAGES=ja, got %q", got)
	}
}

func TestBuildStopTextEnglish(t *testing.T) {
	t.Setenv("AGENTDONE_LANG", "en")
	turn := state.Turn{SessionTitle: "Refactor the parser", Prompt: "run the tests"}
	sum := transcript.Summary{OutputTokens: 27300, Model: "opus-4-8", Skill: "code-review"}
	in := &cchooks.Stop{LastAssistantMessage: "Ran the tests and reported back."}
	got := buildStopText(activeMessages().done, false, 600, turn, in, sum)
	for _, want := range []string{
		"Done (10m 0s)", "Session: Refactor the parser", "Prompt: run the tests",
		"Model: opus-4-8", "Output: 27.3k tok", "Did: Ran the tests",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
