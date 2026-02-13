package runtime

import (
	"os"
	"path/filepath"
)

func ThinHome() string {
	if v := os.Getenv("THIN_HOME"); v != "" {
		return v
	}
	if _, err := os.Stat(".thin"); err == nil {
		wd, _ := os.Getwd()
		return filepath.Join(wd, ".thin")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".thin")
}
