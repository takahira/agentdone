package cli

import (
	"github.com/spf13/cobra"

	"github.com/takahira/agentdone/internal/handler"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Wire the hooks into ~/.claude/settings.json and send a test ping",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return handler.Init()
		},
	}
}
