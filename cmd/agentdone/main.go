// Command agentdone sends low-noise, context-rich Claude Code hook
// notifications to Slack. See package cli for the command tree; with no
// subcommand it runs as a hook and dispatches the stdin payload.
package main

import "github.com/takahira/agentdone/internal/cli"

func main() {
	cli.Execute()
}
