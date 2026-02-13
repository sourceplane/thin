package cmd

import (
	"context"
	"fmt"

	"github.com/sourceplane/thin/internal/runtime"
	"github.com/spf13/cobra"
)

var providerInstallCmd = &cobra.Command{
	Use:   "install <name> <image-ref>",
	Short: "Install a provider from an OCI image",
	Long: `Install a provider from an OCI registry.

Example:
  thin provider install lite ghcr.io/sourceplane/lite-ci:v0.1.2`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		imageRef := args[1]

		ctx := context.Background()
		if err := runtime.PullProviderOCI(ctx, imageRef, name); err != nil {
			fmt.Fprintf(cmd.OutOrStderr(), "✗ Failed to install provider: %v\n", err)
			return err
		}

		return nil
	},
}

func init() {
	providerCmd.AddCommand(providerInstallCmd)
}
