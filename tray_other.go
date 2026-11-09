//go:build !windows && ((!linux && !darwin) || !cgo)

package main

import "fmt"
import "os"

func newTray(logPath string) Tray { return noopTray{} }

// pumpMessages has no meaning outside Windows.
func pumpMessages() bool { return false }

func showError(msg string) { fmt.Fprintln(os.Stderr, msg) }

func showInfo(msg string) { fmt.Fprintln(os.Stderr, msg) }

func openTarget(target string) error { fmt.Fprintln(os.Stderr, "open:", target); return nil }

// askYesNo has no dialog outside Windows; the answer is always "no".
func askYesNo(msg string) bool { return false }
