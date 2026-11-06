//go:build linux

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
)

type linuxClipboard struct{ cmd string }

func newClipboard() (Clipboard, error) {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		if p, err := exec.LookPath("wl-paste"); err == nil {
			return &linuxClipboard{cmd: p}, nil
		}
	}
	if p, err := exec.LookPath("xclip"); err == nil {
		return &linuxClipboard{cmd: p}, nil
	}
	return nil, errors.New("install wl-clipboard (Wayland) or xclip (X11)")
}
func (l *linuxClipboard) Read() (string, error) {
	args := []string{"-selection", "clipboard", "-o"}
	if strings.Contains(l.cmd, "wl-paste") {
		args = []string{"--no-newline"}
	}
	out, err := exec.Command(l.cmd, args...).Output()
	return string(out), err
}
func (l *linuxClipboard) Write(text string) error {
	args := []string{"-selection", "clipboard", "-i"}
	if strings.Contains(l.cmd, "wl-paste") {
		args = nil
	}
	cmdName := l.cmd
	if strings.Contains(l.cmd, "wl-paste") {
		p, err := exec.LookPath("wl-copy")
		if err != nil {
			return err
		}
		cmdName = p
	}
	cmd := exec.Command(cmdName, args...)
	cmd.Stdin = bytes.NewBufferString(text)
	return cmd.Run()
}
func (l *linuxClipboard) Close() error { return nil }

// Non-Windows implementation is best effort. The app currently supports Windows only.
func (c *linuxClipboard) ReplaceIfUnchanged(expected, translated string) (bool, error) {
	current, err := c.Read()
	if err != nil {
		return false, err
	}
	if current != expected {
		return false, nil
	}
	return true, c.Write(translated)
}
