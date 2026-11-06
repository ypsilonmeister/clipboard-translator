//go:build !windows

package main

import "fmt"
import "os"

func newTray(logPath string) Tray { return noopTray{} }

// pumpMessages has no meaning outside Windows.
func pumpMessages() bool { return false }

func showError(msg string) { fmt.Fprintln(os.Stderr, msg) }

// askYesNo has no dialog outside Windows; the answer is always "no".
func askYesNo(msg string) bool { return false }
