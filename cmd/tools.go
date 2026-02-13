package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sourceplane/thin/internal/runtime"
	"github.com/spf13/cobra"
)

var allProviders bool

var toolsCmd = &cobra.Command{
	Use:   "tools",
	Short: "List tools from the active provider",
	RunE: func(cmd *cobra.Command, args []string) error {
		if allProviders {
			return listAllProviderTools()
		}
		return listActiveProviderTools()
	},
}

func listActiveProviderTools() error {
	dir, err := runtime.ActiveProviderToolsDir()
	if err != nil {
		return err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, e := range entries {
		if !e.IsDir() {
			fmt.Println(e.Name())
		}
	}
	return nil
}

func listAllProviderTools() error {
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
		toolsDir := filepath.Join(
			runtime.ThinHome(),
			"providers",
			p.Namespace,
			p.Name,
			p.Version,
			"tools",
		)

		entries, err := os.ReadDir(toolsDir)
		if err != nil {
			continue
		}

		marker := ""
		if active != nil && active.Namespace == p.Namespace && active.Name == p.Name && active.Version == p.Version {
			marker = " (active)"
		}

		fmt.Printf("%s/%s@%s%s:\n", p.Namespace, p.Name, p.Version, marker)

		for _, e := range entries {
			if !e.IsDir() {
				fmt.Printf("  %s\n", e.Name())
			}
		}
		fmt.Println()
	}
	return nil
}

func init() {
	toolsCmd.Flags().BoolVarP(&allProviders, "all-providers", "A", false, "List tools from all providers")
}
