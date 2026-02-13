package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type ProviderRef struct {
	Namespace string `yaml:"namespace"`
	Name      string `yaml:"name"`
	Version   string `yaml:"version"`
}

func ParseProviderRef(ref string) (*ProviderRef, error) {
	parts := strings.Split(ref, "@")
	if len(parts) != 2 {
		return nil, errors.New("invalid provider reference")
	}

	nsName := strings.Split(parts[0], "/")
	if len(nsName) != 2 {
		return nil, errors.New("invalid provider reference")
	}

	return &ProviderRef{
		Namespace: nsName[0],
		Name:      nsName[1],
		Version:   parts[1],
	}, nil
}

func activeProviderPath() string {
	return filepath.Join(ThinHome(), "active-provider.yaml")
}

func WriteActiveProvider(ref *ProviderRef) error {
	b, err := yaml.Marshal(ref)
	if err != nil {
		return err
	}
	return os.WriteFile(activeProviderPath(), b, 0644)
}

func ReadActiveProvider() (*ProviderRef, error) {
	b, err := os.ReadFile(activeProviderPath())
	if err != nil {
		return nil, errors.New("no active provider set")
	}
	var ref ProviderRef
	if err := yaml.Unmarshal(b, &ref); err != nil {
		return nil, err
	}
	return &ref, nil
}

func ActiveProviderToolsDir() (string, error) {
	ref, err := ReadActiveProvider()
	if err != nil {
		return "", err
	}
	return filepath.Join(
		ThinHome(),
		"providers",
		ref.Namespace,
		ref.Name,
		ref.Version,
		"tools",
	), nil
}

func ListProviders() ([]*ProviderRef, error) {
	providersDir := filepath.Join(ThinHome(), "providers")

	// Check if providers directory exists
	if _, err := os.Stat(providersDir); err != nil {
		if os.IsNotExist(err) {
			return []*ProviderRef{}, nil
		}
		return nil, err
	}

	var providers []*ProviderRef

	// Iterate: namespace -> name -> version
	namespaces, err := os.ReadDir(providersDir)
	if err != nil {
		return nil, err
	}

	for _, nsEntry := range namespaces {
		if !nsEntry.IsDir() {
			continue
		}
		namespace := nsEntry.Name()

		providerNames, err := os.ReadDir(filepath.Join(providersDir, namespace))
		if err != nil {
			continue
		}

		for _, nameEntry := range providerNames {
			if !nameEntry.IsDir() {
				continue
			}
			name := nameEntry.Name()

			versions, err := os.ReadDir(filepath.Join(providersDir, namespace, name))
			if err != nil {
				continue
			}

			for _, versionEntry := range versions {
				if !versionEntry.IsDir() {
					continue
				}
				version := versionEntry.Name()

				providers = append(providers, &ProviderRef{
					Namespace: namespace,
					Name:      name,
					Version:   version,
				})
			}
		}
	}

	return providers, nil
}
