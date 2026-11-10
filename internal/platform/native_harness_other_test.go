//go:build !darwin || !cgo || !mactest

package platform

func runPlatformSmokeTests() bool { return false }
