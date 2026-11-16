package platform

// integrityOps are the OS pieces of the "is that window elevated?" check, so the
// comparison is tested without a desktop.
type integrityOps struct {
	// windowLevel is the integrity level of the process that owns win. denied
	// means the process could not be opened because access was refused; ok is
	// false for any other failure (for example the window is already gone).
	windowLevel func(win uintptr) (level uint32, denied, ok bool)
	selfLevel   func() (level uint32, ok bool)
}

// windowMoreElevated reports whether synthetic keys for win would be dropped by
// UIPI: its process runs at a higher integrity level than ours. A process we
// may not even open counts as higher (that is what an elevated one looks like
// to a medium-integrity caller). Other failures count as "not higher", so the
// normal copy path runs and its own timeout still applies.
func windowMoreElevated(win uintptr, ops integrityOps) bool {
	if win == 0 || ops.windowLevel == nil || ops.selfLevel == nil {
		return false
	}
	level, denied, ok := ops.windowLevel(win)
	if denied {
		return true
	}
	if !ok {
		return false
	}
	self, ok := ops.selfLevel()
	return ok && level > self
}
