package cli

import (
	"github.com/spf13/cobra"

	"github.com/takahira/agentdone/internal/handler"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose hook wiring, webhook and state (sends nothing)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return handler.Doctor(cmd.OutOrStdout())
		},
	}
}
