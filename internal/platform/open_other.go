//go:build !windows

package platform

import (
	"errors"
)

func startFallbackEditor(path string) error {
	return errors.New("no fallback editor")
}
