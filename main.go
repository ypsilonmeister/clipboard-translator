// Command ClipboardTranslator is a tray app that translates the selected text
// with a local model; the program itself is in internal/app.
package main

import (
	"flag"
	"runtime"

	"github.com/ypsilonmeister/clipboard-translator/internal/app"
)

// version is the release this binary was built from. release.yml sets it with
// -ldflags "-X main.version=<tag without v>"; a local build stays "dev".
var version = "dev"

func main() {
	flag.Parse()
	// Native desktop APIs must be initialized and serviced on this thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	app.Run(app.Options{
		Version:      version,
		SetupWindows: embeddedSetup,
		SetupLinux:   embeddedLinuxSetup,
		SetupMac:     embeddedMacSetup,
	})
}
