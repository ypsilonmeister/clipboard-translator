//go:build darwin

package main

import (
	"bytes"
	"os/exec"
)

type macClipboard struct{}

func newClipboard() (Clipboard, error) { return &macClipboard{}, nil }
func (m *macClipboard) Read() (string, error) {
	out, err := exec.Command("/usr/bin/pbpaste").Output()
	return string(out), err
}
func (m *macClipboard) Write(text string) error {
	cmd := exec.Command("/usr/bin/pbcopy")
	cmd.Stdin = bytes.NewBufferString(text)
	return cmd.Run()
}
func (m *macClipboard) Close() error { return nil }

// Non-Windows implementation is best effort. The app currently supports Windows only.
func (c *macClipboard) ReplaceIfUnchanged(expected, translated string) (bool, error) {
	current, err := c.Read()
	if err != nil {
		return false, err
	}
	if current != expected {
		return false, nil
	}
	return true, c.Write(translated)
}
