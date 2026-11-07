//go:build darwin && !cgo

package main

func beginSetupProgress(string) {}
func setupCancelled() bool      { return false }
func endSetupProgress()         {}
