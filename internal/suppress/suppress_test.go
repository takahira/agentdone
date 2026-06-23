package suppress

import (
	"testing"

	"github.com/takahira/agentdone/pkg/cchooks"
)

func TestWaitingOnBackground(t *testing.T) {
	cases := []struct {
		name  string
		tasks []cchooks.BackgroundTask
		want  bool
	}{
		{"empty", nil, false},
		{"running", []cchooks.BackgroundTask{{Status: "running"}}, true},
		{"pending", []cchooks.BackgroundTask{{Status: "pending"}}, true},
		{"completed only", []cchooks.BackgroundTask{{Status: "completed"}}, false},
		{"mix of completed and running", []cchooks.BackgroundTask{{Status: "completed"}, {Status: "running"}}, true},
		// an unrecognized in-flight status must count as running, so a
		// new/queued task can't leak a premature "done" ping.
		{"unknown in-flight status (queued)", []cchooks.BackgroundTask{{Status: "queued"}}, true},
		{"empty status on a present task", []cchooks.BackgroundTask{{Status: ""}}, true},
	}
	for _, c := range cases {
		if got := WaitingOnBackground(c.tasks); got != c.want {
			t.Errorf("%s: WaitingOnBackground = %v, want %v", c.name, got, c.want)
		}
	}
}
