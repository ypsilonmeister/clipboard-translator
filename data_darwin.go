package main

import (
	"os"
	"path/filepath"
)

// App bundles may be read-only or translocated by Gatekeeper. Keep downloaded
// runtimes, settings and logs outside the signed bundle.
func writableAppDir(string) (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "ClipboardTranslator")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}
