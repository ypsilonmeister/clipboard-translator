//go:build !windows && ((!linux && !darwin) || !cgo)

package platform

import "fmt"
import "os"

func NewTray(logPath string) Tray { return noopTray{} }

// PumpMessages has no meaning outside Windows.
func PumpMessages() bool { return false }

func ShowError(msg string) { fmt.Fprintln(os.Stderr, msg) }

func ShowInfo(msg string) { fmt.Fprintln(os.Stderr, msg) }

func OpenTarget(target string) error { fmt.Fprintln(os.Stderr, "open:", target); return nil }

// AskYesNo has no dialog outside Windows; the answer is always "no".
func AskYesNo(msg string) bool { return false }
