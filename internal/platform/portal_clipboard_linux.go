package platform

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// These non-blocking Clipboard methods return errPortalPending until acknowledged.
// W4 must retry them on UI ticks; never spin or sleep in the application serve loop.
type portalClipboard struct {
	offeredText              string
	remote                   *portalRemote
	seq                      uint32
	mime                     string
	known, owner, cacheValid bool
	ownedText, cached        string
	read                     *portalRead
	write                    *portalWrite
	transfers                map[string]uint32
}
type portalRead struct {
	token, mime, text string
	done              bool
	code              uint32
	seq               uint32
}
type portalWrite struct {
	token, text, expected       string
	compare, ack, seenOwn, done bool
	err                         error
	deadline                    time.Time
}

func (c *portalClipboard) reset() {
	if c.read != nil {
		c.remote.wire.CancelIO(c.read.token)
	}
	for token := range c.transfers {
		c.remote.wire.CancelIO(token)
	}
	c.read = nil
	c.write = nil
	c.transfers = make(map[string]uint32)
	c.owner = false
	c.known = false
	c.cacheValid = false
	c.ownedText = ""
	c.offeredText = ""
	c.cached = ""
	c.mime = portalTextMIME
	c.seq++
}
func (c *portalClipboard) ready() error {
	if c.remote.phase != "ready" {
		return errPortalNotAuthorized
	}
	return nil
}
func (c *portalClipboard) Read() (string, error) {
	c.remote.Step()
	if err := c.ready(); err != nil {
		return "", err
	}
	if c.owner {
		return c.ownedText, nil
	}
	if c.cacheValid {
		return c.cached, nil
	}
	if c.known && c.mime == "" {
		return "", nil
	}
	if c.read != nil {
		op := c.read
		if !op.done {
			return "", errPortalPending
		}
		c.read = nil
		if op.code == 2 && op.mime == portalTextMIME {
			c.startRead("text/plain")
			return "", errPortalPending
		}
		if op.code == 2 {
			return "", errPortalNoText
		}
		if op.code != 0 {
			return "", errors.New("could not read the Wayland clipboard")
		}
		if !utf8.ValidString(op.text) || strings.IndexByte(op.text, 0) >= 0 {
			return "", errors.New("Wayland clipboard text is not valid UTF-8")
		}
		if op.seq != c.seq {
			return "", errPortalPending
		}
		c.cached = op.text
		c.cacheValid = true
		return c.cached, nil
	}
	c.startRead(c.mime)
	return "", errPortalPending
}
func (c *portalClipboard) startRead(mime string) {
	token, err := portalToken()
	if err != nil {
		c.read = &portalRead{done: true, code: 1}
		return
	}
	c.read = &portalRead{token: token, mime: mime, seq: c.seq}
	c.remote.wire.ReadSelection(c.remote.session, token, mime)
}
func (c *portalClipboard) Write(text string) error {
	_, err := c.writeText("", text, false)
	return err
}
func (c *portalClipboard) ReplaceIfUnchanged(expected, text string) (bool, error) {
	return c.writeText(expected, text, true)
}
func (c *portalClipboard) writeText(expected, text string, compare bool) (bool, error) {
	c.remote.Step()
	if err := c.ready(); err != nil {
		return false, err
	}
	if len(text) > portalTextLimit || !utf8.ValidString(text) || strings.IndexByte(text, 0) >= 0 {
		return false, errors.New("invalid or oversized Wayland clipboard text")
	}
	if c.write != nil {
		op := c.write
		if !op.done {
			return false, errPortalPending
		}
		c.write = nil
		if op.text == text && op.expected == expected && op.compare == compare {
			if compare && errors.Is(op.err, errPortalClipboardChanged) {
				return false, nil
			}
			return op.err == nil, op.err
		}
	}
	if compare {
		current, err := c.Read()
		if err != nil {
			return false, err
		}
		if !SameClipboardText(current, expected) {
			return false, nil
		}
	}
	token, err := portalToken()
	if err != nil {
		return false, errors.New("could not create a clipboard write identifier")
	}
	c.write = &portalWrite{token: token, text: text, expected: expected, compare: compare, deadline: c.remote.client.now().Add(5 * time.Second)}
	c.offeredText = text
	c.remote.wire.SetSelection(c.remote.session, token)
	return false, errPortalPending
}
func (c *portalClipboard) Close() error { c.remote.Close(); return nil }
func (c *portalClipboard) tick() {
	if c.write != nil && !c.write.done && !c.remote.client.now().Before(c.write.deadline) {
		c.write.done = true
		c.write.err = errors.New("Wayland clipboard write timed out")
	}
}
func (c *portalClipboard) event(e portalEvent) bool {
	if c.write != nil && c.write.token == e.token && (e.kind == "method" || e.kind == "failed") {
		if e.kind == "failed" || e.code != 0 {
			c.write.done = true
			c.write.err = errors.New("could not write the Wayland clipboard")
		} else {
			c.write.ack = true
			if c.write.seenOwn {
				c.write.done = true
			}
		}
		return true
	}
	if c.read != nil && c.read.token == e.token && e.kind == "read" {
		c.read.done = true
		c.read.code = e.code
		c.read.text = string(e.data)
		return true
	}
	if serial, ok := c.transfers[e.token]; ok && e.kind == "written" {
		delete(c.transfers, e.token)
		c.remote.wire.WriteDone(c.remote.session, serial, e.code == 0)
		return true
	}
	if e.session != c.remote.session || c.remote.session == "" {
		return false
	}
	switch e.kind {
	case "selection-owner":
		c.seq++
		c.mime = e.mime
		c.known = true
		c.cacheValid = false
		if c.read != nil {
			c.remote.wire.CancelIO(c.read.token)
			c.read = nil
		}
		c.owner = e.owner
		if e.owner {
			c.ownedText = c.offeredText
		}
		if e.owner && c.write != nil && !c.write.done {
			c.write.seenOwn = true
			if c.write.ack {
				c.write.done = true
			}
		} else if !e.owner {
			c.ownedText = ""
			if c.write != nil && c.write.seenOwn {
				c.write.done = true
				c.write.err = errPortalClipboardChanged
			}
		}
		return true
	case "selection-transfer":
		if !c.owner || !isPortalTextMIME(e.mime) || len(c.transfers) >= 8 || c.remote.phase != "ready" {
			c.remote.wire.WriteDone(c.remote.session, e.serial, false)
			return true
		}
		token, err := portalToken()
		if err != nil {
			c.remote.wire.WriteDone(c.remote.session, e.serial, false)
			return true
		}
		c.transfers[token] = e.serial
		c.remote.wire.WriteSelection(c.remote.session, token, e.serial, []byte(c.ownedText))
		return true
	}
	return false
}
func isPortalTextMIME(mime string) bool {
	m := strings.ToLower(mime)
	return m == "text/plain" || m == portalTextMIME
}
