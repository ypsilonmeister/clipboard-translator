//go:build !linux || !cgo

package platform

func InitializeDesktop(string) error { return nil }
func CloseDesktop()                  {}
