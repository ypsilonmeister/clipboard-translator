// Command ClipboardTranslator is a tray app that translates the selected text
// with a local model; the program itself is in internal/app.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/ypsilonmeister/clipboard-translator/internal/app"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
	"github.com/ypsilonmeister/clipboard-translator/internal/setup"
)

// version is the release this binary was built from. release.yml sets it with
// -ldflags "-X main.version=<tag without v>"; a local build stays "dev".
var version = "dev"

func main() {
	// "clipboard-translator setup" is the first-run installer (Issue #152);
	// it runs in the console and exits, before any GUI state exists. The
	// setup scripts are thin wrappers around it.
	if len(os.Args) > 1 && os.Args[1] == "setup" {
		os.Exit(runSetup(os.Args[2:]))
	}
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

// runSetup parses the subcommand's flags and runs internal/setup. Naming
// a backend on the command line is an explicit choice (only it may
// rewrite an existing install's engine settings); the wrappers forward
// --backend only for an explicit choice, exactly as the scripts did.
func runSetup(args []string) int {
	attachParentConsole()
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	backend := fs.String("backend", "", "llama.cpp backend to install (platform-specific; Windows also accepts \"auto\")")
	model := fs.String("model", "", "model size to install: 0.8b, 2b or 8b (default: keep config.json's model, 2b on a fresh install)")
	root := fs.String("root", "", "app folder to set up (default: the app's own folder)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: clipboard-translator setup [--backend <name>] [--model <0.8b|2b|8b>] [--root <dir>]")
		fmt.Fprintln(os.Stderr, "Downloads the PLaMo model and llama.cpp and writes config.json.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return 2
	}
	explicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "backend" && *backend != "auto" {
			explicit = true
		}
	})
	dir := *root
	if dir == "" {
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintln(os.Stderr, "setup:", err)
			return 1
		}
		if dir, err = platform.WritableAppDir(filepath.Dir(exe)); err != nil {
			fmt.Fprintln(os.Stderr, "setup:", err)
			return 1
		}
	}
	return setup.Run(setup.Options{
		Root:            dir,
		Backend:         *backend,
		ExplicitBackend: explicit,
		Model:           *model,
	})
}
