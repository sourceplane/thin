package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	gruntime "runtime"
	"strings"
	"text/template"

	"github.com/sourceplane/thin/internal/runtime"
	"github.com/spf13/cobra"
)

var version = "dev"

// TemplateContext holds variables available for template substitution in manifest
type TemplateContext struct {
	ProviderHome string // Root directory of the provider
	ProviderName string // Name of the provider
	ProviderVersion string // Version of the provider
	OS string // Current operating system
	Arch string // Current architecture
}

var rootCmd = &cobra.Command{
	Use:   "thin",
	Short: "Execute provider commands",
	Long: `thin executes provider commands.
Providers are single-tool executables that handle all operations.
Usage: thin <provider> [command] [args...]`,
	SilenceErrors: true,
	SilenceUsage:  true,
}

func Execute() {
	args := os.Args[1:]
	
	// Parse global flags first
	for i := 0; i < len(args); i++ {
		arg := args[i]
		
		// Stop parsing flags at first non-flag argument
		if len(arg) > 0 && arg[0] != '-' {
			break
		}
	}

	// Check if first remaining arg is a provider reference (namespace/name@version)
	if len(args) > 0 {
		arg := args[0]
		providerRef, err := runtime.ParseProviderRef(arg)
		if err == nil {
			// First arg is a valid provider reference
			if len(args) > 1 {
				// Provider ref followed by command/args
				cmdArgs := args[1:]
				
				if err := executeProviderCommand(providerRef, cmdArgs); err != nil {
					fmt.Fprintf(os.Stderr, "Error: %v\n", err)
					os.Exit(1)
				}
				return
			} else {
				// Provider ref alone, treat as `use` command
				if err := runtime.WriteActiveProvider(providerRef); err != nil {
					if err := rootCmd.Execute(); err != nil {
						os.Exit(1)
					}
					return
				}
				// Success, exit
				return
			}
		}
	}

	// Check if first remaining arg is a provider name (use active provider)
	if len(args) > 0 {
		arg := args[0]
		// Skip reserved commands - let Cobra handle these
		if arg != "tools" && arg != "provider" && arg != "providers" && arg != "use" && arg != "help" && arg != "completion" && arg != "version" {
			// Try to resolve as a provider name
			providerRef, err := runtime.ParseProviderRef(arg)
			if err != nil {
				// Not a valid provider ref, might be a simple name
				// Try to find it as an installed provider
				providerRef, err = resolveProviderByName(arg)
				if err == nil {
					// Provider found, execute command with remaining args
					if len(args) > 1 {
						cmdArgs := args[1:]
						if err := executeProviderCommand(providerRef, cmdArgs); err != nil {
							fmt.Fprintf(os.Stderr, "Error: %v\n", err)
							os.Exit(1)
						}
						return
					}
				}
			} else {
				// Valid provider ref
				if len(args) > 1 {
					cmdArgs := args[1:]
					if err := executeProviderCommand(providerRef, cmdArgs); err != nil {
						fmt.Fprintf(os.Stderr, "Error: %v\n", err)
						os.Exit(1)
					}
					return
				}
			}
		}
	}

	// Fall through to normal Cobra execution
	rootCmd.SetArgs(args)
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// executeProviderCommand reads the provider manifest and executes the entrypoint with command args
func executeProviderCommand(providerRef *runtime.ProviderRef, cmdArgs []string) error {
	providerDir := filepath.Join(
		runtime.ThinHome(),
		"providers",
		providerRef.Name, // Simplified: no namespace in path for now
	)

	// Read provider manifest
	manifest, err := runtime.ReadProviderManifest(providerDir)
	if err != nil {
		return fmt.Errorf("failed to read provider manifest: %w", err)
	}

	if manifest == nil {
		return fmt.Errorf("provider manifest not found")
	}

	// Get entrypoint configuration
	entrypoint := manifest.Entrypoint.Executable
	if entrypoint == "" {
		entrypoint = "entrypoint"
	}

	// Resolve full path to binary
	binaryPath := filepath.Join(providerDir, "bin", entrypoint)
	
	if _, err := os.Stat(binaryPath); err != nil {
		// Try alternate location for multi-platform
		binaryPath, err = runtime.GetPlatformBinaryPath(providerDir)
		if err != nil {
			return fmt.Errorf("binary not found: %w", err)
		}
	}

	// Build command arguments
	var finalArgs []string
	
	// Add default args from manifest if present
	if manifest.Entrypoint.DefaultArgs != "" {
		// Create template context with provider information
		ctx := TemplateContext{
			ProviderHome: providerDir,
			ProviderName: providerRef.Name,
			ProviderVersion: providerRef.Version,
			OS: gruntime.GOOS,
			Arch: gruntime.GOARCH,
		}
		
		// Process default args through template
		processedArgs, err := processTemplate(manifest.Entrypoint.DefaultArgs, ctx)
		if err != nil {
			return fmt.Errorf("failed to process default args template: %w", err)
		}
		
		// Parse processed args (handle quoted strings)
		defaultArgs := parseArgs(processedArgs)
		finalArgs = append(finalArgs, defaultArgs...)
	}
	
	// Add command arguments
	finalArgs = append(finalArgs, cmdArgs...)

	// Execute the binary
	return runtime.ExecTool(binaryPath, finalArgs)
}

// processTemplate evaluates template variables in a string
func processTemplate(templateStr string, ctx TemplateContext) (string, error) {
	tmpl, err := template.New("args").Parse(templateStr)
	if err != nil {
		return "", fmt.Errorf("invalid template: %w", err)
	}
	
	var result strings.Builder
	if err := tmpl.Execute(&result, ctx); err != nil {
		return "", fmt.Errorf("template execution failed: %w", err)
	}
	
	return result.String(), nil
}

// resolveProviderByName finds a provider by name from installed providers
func resolveProviderByName(name string) (*runtime.ProviderRef, error) {
	// Since we're using flat directory structure (providers/name), check directly
	providerDir := filepath.Join(runtime.ThinHome(), "providers", name)
	
	// Check if provider directory exists
	if stat, err := os.Stat(providerDir); err == nil && stat.IsDir() {
		// Found it - return a provider ref with just the name
		return &runtime.ProviderRef{
			Namespace: "local", // Default namespace for flat structure
			Name:      name,
			Version:   "latest", // Default version for flat structure
		}, nil
	}

	// If not found as flat, try the old nested structure (providers/namespace/name/version)
	providers, err := runtime.ListProviders()
	if err != nil {
		return nil, err
	}

	for _, p := range providers {
		if p.Name == name {
			return p, nil
		}
	}

	return nil, fmt.Errorf("provider '%s' not found", name)
}

// parseArgs parses a command line string into individual arguments, handling quoted strings
func parseArgs(argString string) []string {
	var args []string
	var current strings.Builder
	inQuote := false
	var quoteChar rune

	for _, ch := range argString {
		if (ch == '"' || ch == '\'') && !inQuote {
			// Starting a quoted section
			inQuote = true
			quoteChar = ch
		} else if ch == quoteChar && inQuote {
			// Ending a quoted section
			inQuote = false
		} else if ch == ' ' && !inQuote {
			// Space outside quotes - separator
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		} else {
			// Regular character
			current.WriteRune(ch)
		}
	}

	if current.Len() > 0 {
		args = append(args, current.String())
	}

	return args
}

func init() {
	rootCmd.Version = version
	rootCmd.SetVersionTemplate("{{.Version}}\n")
	rootCmd.AddCommand(providerCmd)
	rootCmd.AddCommand(providersCmd)
}

