// Package cli wires the cobra command tree. With no subcommand the binary runs
// as a Claude Code hook (reads the event payload on stdin and dispatches it);
// the subcommands (init / uninstall / doctor / version) handle setup and
// diagnosis.
package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/takahira/agentdone/internal/handler"
	"github.com/takahira/agentdone/pkg/cchooks"
)

// Execute builds and runs the root command, exiting non-zero on error.
func Execute() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "agentdone",
		Short: "Low-noise, context-rich Claude Code hook notifications",
		Long: "agentdone sends Slack notifications for Claude Code, but only when\n" +
			"the agent is really done — withholding the premature \"completed\" ping\n" +
			"while background work is still running.\n\n" +
			"With no subcommand it runs as a hook: it reads the event payload on\n" +
			"stdin and dispatches it. Wire it into ~/.claude/settings.json with `init`.",
		Args: cobra.NoArgs,
		// Don't dump full usage on a command's RunE error, but still print the
		// error itself (e.g. "unknown command"). The hook path's RunE always
		// returns nil, so it never produces output.
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return dispatch(os.Stdin)
		},
	}
	root.AddCommand(newInitCmd(), newUninstallCmd(), newDoctorCmd(), newVersionCmd())
	return root
}

// dispatch reads a hook payload and routes it to the matching handler. A hook
// must never block Claude Code, so malformed input is swallowed.
func dispatch(r io.Reader) error {
	ev, err := cchooks.Parse(r)
	if err != nil {
		return nil
	}
	// Run the handler under recovery so a panic degrades to "no notification"
	// rather than crashing Claude Code; see safely for the full contract.
	safely(func() {
		switch e := ev.(type) {
		case *cchooks.Stop:
			handler.Stop(e)
		case *cchooks.StopFailure:
			handler.StopFailure(e)
		case *cchooks.Notification:
			handler.Notification(e)
		case *cchooks.PreToolUse:
			handler.PreToolUse(e)
		case *cchooks.UserPromptSubmit:
			handler.UserPromptSubmit(e)
		}
	})
	return nil
}

// safely runs fn, recovering from and swallowing any panic. A Claude Code hook
// must never crash: a panicking handler degrades to "no notification" instead
// of exiting non-zero, which Claude Code would report as a hook failure. Under
// AGENTDONE_DEBUG the recovered panic is logged to stderr (which Claude Code
// captures) — otherwise a panicking handler is indistinguishable from a turn
// that simply had nothing to say.
func safely(fn func()) {
	defer func() {
		if r := recover(); r != nil && os.Getenv("AGENTDONE_DEBUG") != "" {
			fmt.Fprintf(os.Stderr, "agentdone: handler panic recovered: %v\n", r)
		}
	}()
	fn()
}
