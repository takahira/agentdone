package cli

import (
	"github.com/spf13/cobra"

	"github.com/takahira/agentdone/internal/handler"
)

func newUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove this tool's hook entries from ~/.claude/settings.json",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return handler.Uninstall()
		},
	}
}
