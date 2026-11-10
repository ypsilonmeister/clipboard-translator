package platform

import (
	"net"
	"os"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/testsupport"
)

// TestMain lets the test binary double as a fake llama-server (job_windows_test
// and proc_linux_test start it) and, with -tags mactest, run the native macOS
// checks on the main thread.
func TestMain(m *testing.M) {
	testsupport.MaybeRunFakeServer()
	if runPlatformSmokeTests() {
		return
	}
	os.Exit(m.Run())
}

// testClipboard is an in-memory Clipboard.
type testClipboard struct {
	text   string
	writes int
	err    error
}

func (c *testClipboard) Read() (string, error) { return c.text, nil }
func (c *testClipboard) Write(s string) error  { c.text = s; c.writes++; return nil }
func (c *testClipboard) Close() error          { return nil }
func (c *testClipboard) ReplaceIfUnchanged(expected, translated string) (bool, error) {
	if c.err != nil {
		return false, c.err
	}
	if !SameClipboardText(c.text, expected) {
		return false, nil
	}
	c.text = translated
	c.writes++
	return true, nil
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
