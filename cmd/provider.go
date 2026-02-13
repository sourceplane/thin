package cmd

import (
	"fmt"

	"github.com/sourceplane/thin/internal/runtime"
	"github.com/spf13/cobra"
)

var providerCmd = &cobra.Command{
	Use:   "provider",
	Short: "Manage active provider",
}

var providersCmd = &cobra.Command{
	Use:   "providers",
	Short: "Manage active provider",
	Hidden: true, // Hidden alias for provider
}

var providerUseCmd = &cobra.Command{
	Use:   "use <namespace>/<name>@<version>",
	Short: "Set active provider",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ref, err := runtime.ParseProviderRef(args[0])
		if err != nil {
			return err
		}
		if err := runtime.WriteActiveProvider(ref); err != nil {
			return err
		}
		fmt.Printf("Active provider set to %s/%s@%s\n", ref.Namespace, ref.Name, ref.Version)
		return nil
	},
}

var providerListCmd = &cobra.Command{
	Use:   "list",
	Short: "List installed providers",
	RunE: func(cmd *cobra.Command, args []string) error {
		providers, err := runtime.ListProviders()
		if err != nil {
			return err
		}

		if len(providers) == 0 {
			fmt.Println("No providers installed")
			return nil
		}

		// Get active provider for comparison
		active, _ := runtime.ReadActiveProvider()

		for _, p := range providers {
			marker := "  "
			if active != nil && active.Namespace == p.Namespace && active.Name == p.Name && active.Version == p.Version {
				marker = "* "
			}
			fmt.Printf("%s%s/%s@%s\n", marker, p.Namespace, p.Name, p.Version)
		}
		return nil
	},
}

func init() {
	providerCmd.AddCommand(providerUseCmd)
	providerCmd.AddCommand(providerListCmd)
	
	providersCmd.AddCommand(providerUseCmd)
	providersCmd.AddCommand(providerListCmd)
}
