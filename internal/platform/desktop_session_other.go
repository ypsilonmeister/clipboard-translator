//go:build !linux || !cgo

package platform

func InitializeDesktop(string) error {
	desktopCrostini = evaluateCrostini()
	return nil
}
func CloseDesktop() {}
