package platform

// integrityOps are the OS pieces of the "is that window elevated?" check, so the
// comparison is tested without a desktop.
type integrityOps struct {
	// windowLevel is the integrity level of the process that owns win. denied
	// means the process or its token could not be opened because access was
	// refused; ok is false for any other failure (for example the window is
	// already gone).
	windowLevel func(win uintptr) (level uint32, denied, ok bool)
	selfLevel   func() (level uint32, ok bool)
}

// windowMoreElevated reports whether synthetic keys for win would be dropped by
// UIPI: its process runs at a higher integrity level than ours. A process we
// may not even open, or whose token we may not read, counts as higher (that is
// what a process of another, more privileged account looks like to a
// medium-integrity caller). Other failures count as "not higher", so the
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

// tokenCalls are the system calls behind integrityOps.windowLevel, so that
// which failure counts as "access refused" is tested without a desktop. A zero
// handle is a failure; denied says it was an access-denied one.
type tokenCalls struct {
	pidOf       func(win uintptr) uint32
	openProcess func(pid uint32) (process uintptr, denied bool)
	openToken   func(process uintptr) (token uintptr, denied bool)
	level       func(token uintptr) (uint32, bool)
	closeHandle func(uintptr)
}

// processLevel reads the integrity level of an open process. Being refused the
// token is reported as denied: processes of our own account let us read theirs
// (an elevated one of the same user included), so a refusal means the
// process belongs to an account we have no rights on, such as SYSTEM or the
// administrator account a standard user elevated with. A token that opens but
// cannot be read is only "unknown".
func (c tokenCalls) processLevel(process uintptr) (level uint32, denied, ok bool) {
	token, denied := c.openToken(process)
	if token == 0 {
		return 0, denied, false
	}
	defer c.closeHandle(token)
	level, ok = c.level(token)
	return level, false, ok
}

// windowLevel implements integrityOps.windowLevel.
func (c tokenCalls) windowLevel(win uintptr) (level uint32, denied, ok bool) {
	pid := c.pidOf(win)
	if pid == 0 {
		return 0, false, false
	}
	process, denied := c.openProcess(pid)
	if process == 0 {
		return 0, denied, false
	}
	defer c.closeHandle(process)
	return c.processLevel(process)
}
