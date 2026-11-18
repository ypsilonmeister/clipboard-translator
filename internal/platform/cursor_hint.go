package platform

// CursorHint is a short-lived busy indicator. Show anchors it to the current
// cursor position. All calls, including creation, belong to the UI thread.
type CursorHint interface {
	Show()
	Hide()
	Close()
}

type noopCursorHint struct{}

func (noopCursorHint) Show()  {}
func (noopCursorHint) Hide()  {}
func (noopCursorHint) Close() {}
