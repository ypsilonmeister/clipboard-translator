//go:build !windows

package platform

import (
	"errors"
)

func StartFallbackEditor(path string) error {
	return errors.New("no fallback editor")
}
