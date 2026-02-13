package runtime

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

func ResolveTool(name string) (string, error) {
	dir, err := ActiveProviderToolsDir()
	if err != nil {
		return "", err
	}

	path := filepath.Join(dir, name)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", errors.New("tool not found: " + name)
	}
	return path, nil
}

func ResolveToolWithProvider(name string, provider *ProviderRef) (string, error) {
	dir := filepath.Join(
		ThinHome(),
		"providers",
		provider.Namespace,
		provider.Name,
		provider.Version,
		"tools",
	)

	path := filepath.Join(dir, name)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", errors.New("tool not found: " + name)
	}
	return path, nil
}

func ExecTool(path string, args []string) error {
	cmd := exec.Command(path, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		"THIN_HOME="+ThinHome(),
	)
	return cmd.Run()
}
