//go:build !windows

package main

import "fmt"
import "os"

func newTray(logPath string) Tray { return noopTray{} }

// pumpMessages has no meaning outside Windows.
func pumpMessages() bool { return false }

func showError(msg string) { fmt.Fprintln(os.Stderr, msg) }
