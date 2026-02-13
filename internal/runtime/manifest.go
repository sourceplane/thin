package runtime

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ProviderManifest represents the thin.provider.yaml structure
// Spec: https://github.com/sourceplane/thin/blob/main/oci/thin.provider.yaml
type ProviderManifest struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`

	Metadata struct {
		Name        string        `yaml:"name"`
		Version     string        `yaml:"version"`
		Description string        `yaml:"description"`
		Homepage    string        `yaml:"homepage"`
		Maintainers []interface{} `yaml:"maintainers"` // Can be complex
		License     string        `yaml:"license"`
	} `yaml:"metadata"`

	Distribution struct {
		Type string `yaml:"type"` // "oci"
		Ref  string `yaml:"ref"`  // "ghcr.io/sourceplane/lite-ci"
	} `yaml:"distribution"`

	Runtime struct {
		Default   string        `yaml:"default"`
		Supported []interface{} `yaml:"supported"` // Runtime configurations
	} `yaml:"runtime"`

	Entrypoint struct {
		Executable  string `yaml:"executable"`
		DefaultArgs string `yaml:"defaultArgs"`
	} `yaml:"entrypoint"`

	Platforms []struct {
		OS     string `yaml:"os"`
		Arch   string `yaml:"arch"`
		Binary string `yaml:"binary"`
	} `yaml:"platforms"`

	Layers map[string]interface{} `yaml:"layers"`

	Capabilities map[string]struct {
		Description string `yaml:"description"`
		Lifecycle   struct {
			Stability   string `yaml:"stability"` // stable, experimental, deprecated
			IntroducedIn string `yaml:"introducedIn"`
		} `yaml:"lifecycle"`
		Inputs []struct {
			Name        string      `yaml:"name"`
			Type        string      `yaml:"type"`
			Required    bool        `yaml:"required"`
			Default     interface{} `yaml:"default"`
			Description string      `yaml:"description"`
		} `yaml:"inputs"`
		Outputs []struct {
			Name        string `yaml:"name"`
			Type        string `yaml:"type"`
			Description string `yaml:"description"`
		} `yaml:"outputs"`
	} `yaml:"capabilities"`

	Assets struct {
		Root          string        `yaml:"root"`
		Contains      []string      `yaml:"contains"`
		Immutability  interface{}   `yaml:"immutability"`
	} `yaml:"assets"`

	Architecture struct {
		Pattern   string   `yaml:"pattern"`
		Stages    []string `yaml:"stages"`
		Principles []string `yaml:"principles"`
	} `yaml:"architecture"`

	Models map[string]interface{} `yaml:"models"`
}

// ReadProviderManifest reads and parses the thin.provider.yaml file
// Returns error if manifest exists but is invalid
// If manifest doesn't exist, returns nil (manifest is optional)
func ReadProviderManifest(providerDir string) (*ProviderManifest, error) {
	manifestPath := filepath.Join(providerDir, "thin.provider.yaml")

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		// Manifest is optional - only error if it exists but can't be read
		return nil, nil
	}

	var manifest ProviderManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest: %w", err)
	}

	// Validate required fields if manifest was found
	if err := manifest.Validate(); err != nil {
		return nil, err
	}

	return &manifest, nil
}

// Validate checks that all required fields are present and valid
func (m *ProviderManifest) Validate() error {
	if m.APIVersion == "" {
		return fmt.Errorf("manifest missing required field: apiVersion")
	}
	if m.APIVersion != "thin.io/v1" {
		return fmt.Errorf("unsupported apiVersion: %s (expected: thin.io/v1)", m.APIVersion)
	}
	if m.Kind != "Provider" {
		return fmt.Errorf("manifest kind must be 'Provider', got: %s", m.Kind)
	}
	if m.Metadata.Name == "" {
		return fmt.Errorf("manifest missing required field: metadata.name")
	}
	if m.Metadata.Version == "" {
		return fmt.Errorf("manifest missing required field: metadata.version")
	}
	if m.Distribution.Type == "" {
		return fmt.Errorf("manifest missing required field: distribution.type")
	}
	if m.Distribution.Type != "oci" {
		return fmt.Errorf("unsupported distribution type: %s (expected: oci)", m.Distribution.Type)
	}
	if m.Distribution.Ref == "" {
		return fmt.Errorf("manifest missing required field: distribution.ref")
	}
	if m.Entrypoint.Executable == "" {
		return fmt.Errorf("manifest missing required field: entrypoint.executable")
	}
	if len(m.Capabilities) == 0 {
		return fmt.Errorf("manifest must define at least one capability")
	}
	return nil
}

// GetCapabilities returns the list of capability names for a provider
// Returns empty list if manifest doesn't exist
func GetCapabilities(providerDir string) ([]string, error) {
	manifest, err := ReadProviderManifest(providerDir)
	if err != nil {
		return nil, err
	}

	// Manifest is optional
	if manifest == nil {
		return []string{}, nil
	}

	capabilities := make([]string, 0, len(manifest.Capabilities))
	for name := range manifest.Capabilities {
		capabilities = append(capabilities, name)
	}

	return capabilities, nil
}

// GetProviderMetadata returns basic provider information
// Returns empty strings if manifest doesn't exist
func GetProviderMetadata(providerDir string) (name, version string, err error) {
	manifest, err := ReadProviderManifest(providerDir)
	if err != nil {
		return "", "", err
	}

	// Manifest is optional
	if manifest == nil {
		return "", "", nil
	}

	return manifest.Metadata.Name, manifest.Metadata.Version, nil
}
