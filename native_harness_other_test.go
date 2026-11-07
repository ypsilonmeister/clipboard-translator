//go:build !darwin || !cgo || !mactest

package main

func runPlatformSmokeTests() bool { return false }
