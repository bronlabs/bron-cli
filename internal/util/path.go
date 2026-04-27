package util

import (
	"os"
	"path/filepath"
	"strings"
)

// Expand resolves a leading ~/ to the user's home directory.
func Expand(p string) (string, error) {
	if !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, p[2:]), nil
}
