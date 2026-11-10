//go:build !windows

package main

// Other platforms always have a console when setup runs in a terminal.
func attachParentConsole() {}
