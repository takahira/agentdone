package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// version is overridden at release time via -ldflags
// "-s -w -X github.com/takahira/agentdone/internal/cli.version=X.Y.Z".
// (goreleaser's {{ .Version }} strips the leading 'v' from the git tag.)
var version = "dev"

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Printf("agentdone %s\n", version)
		},
	}
}
