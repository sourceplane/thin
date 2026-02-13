package cmd

import (
	"fmt"

	"github.com/sourceplane/thin/internal/runtime"
	"github.com/spf13/cobra"
)

var useCmd = &cobra.Command{
	Use:   "use <namespace>/<name>@<version> [tool] [tool_args...]",
	Short: "Set active provider and optionally execute a tool",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Parse provider reference
		ref, err := runtime.ParseProviderRef(args[0])
		if err != nil {
			return err
		}

		// If no tool specified, just set the provider
		if len(args) == 1 {
			if err := runtime.WriteActiveProvider(ref); err != nil {
				return err
			}
			fmt.Printf("Active provider set to %s/%s@%s\n", ref.Namespace, ref.Name, ref.Version)
			return nil
		}

		// Tool specified, set provider and execute tool
		if err := runtime.WriteActiveProvider(ref); err != nil {
			return err
		}

		toolName := args[1]
		toolArgs := args[2:]

		toolPath, err := runtime.ResolveToolWithProvider(toolName, ref)
		if err != nil {
			return err
		}

		return runtime.ExecTool(toolPath, toolArgs)
	},
}

func init() {
	rootCmd.AddCommand(useCmd)
}
