//go:build darwin && !cgo

package platform

func beginSetupProgress(string) {}
func setupCancelled() bool      { return false }
func endSetupProgress()         {}
