package app

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/languages"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
	"github.com/ypsilonmeister/clipboard-translator/internal/textstruct"
)

// version is the release this binary was built from, set by Run from
// Options.Version (the root package's -X main.version stamp); "dev" in tests
// and local builds.
var version = "dev"

// Options is what the main package hands to Run: the version string and the
// setup scripts it embeds (go:embed cannot reach the repository root from here).
type Options struct {
	Version                            string
	SetupWindows, SetupLinux, SetupMac []byte
}

type result struct {
	original, translated string
	generation           uint64
	err                  error
	direction            languages.Direction
	window               uintptr // foreground window when the hotkey was pressed
	fromSelection        bool    // the text was a fresh selection, not the old clipboard
	output               string
	capturedAt           time.Time
}

func appDir() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(p), nil
}

// ignoreWriteErrors lets log output go to stdout when there is one. In the
// GUI build (-H=windowsgui) stdout is invalid and every write fails, which
// would otherwise make io.MultiWriter stop before reaching the log file.
type ignoreWriteErrors struct{ w io.Writer }

func (i ignoreWriteErrors) Write(p []byte) (int, error) {
	_, _ = i.w.Write(p)
	return len(p), nil
}

// fatal logs the message, shows it to the user (message box in the GUI build)
// and exits.
func fatal(v ...any) {
	msg := fmt.Sprint(v...)
	log.Print(msg)
	platform.ShowError(msg)
	os.Exit(1)
}

// Run is the program: it returns when the user quits (or has declined setup).
// It must be called on the process's main goroutine, which the caller has
// locked to its OS thread: the tray window and the hotkeys belong to it.
func Run(opts Options) {
	if opts.Version != "" {
		version = opts.Version
	}
	embeddedSetup, embeddedLinuxSetup, embeddedMacSetup = opts.SetupWindows, opts.SetupLinux, opts.SetupMac
	if err := platform.PlatformRuntimeError(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Windows RegisterHotKey and the tray window are thread-local; the same OS
	// thread must create them and pump their messages.
	dir, err := appDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	dir, err = platform.WritableAppDir(dir)
	if err != nil {
		fatal("app data folder: ", err)
	}
	logPath := filepath.Join(dir, "translator.log")
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		platform.ShowError(err.Error())
		os.Exit(1)
	}
	defer logf.Close()
	log.SetOutput(io.MultiWriter(logf, ignoreWriteErrors{os.Stdout}))
	log.SetFlags(log.Ldate | log.Ltime)
	conf, err := config.Load(dir)
	if err != nil {
		fatal("config: ", err)
	}

	// Prevent concurrent application instances.
	instance, err := net.Listen("tcp", "127.0.0.1:"+fmt.Sprint(conf.Port+1))
	if err != nil {
		fatal("another instance or conflicting port is running: ", err)
	}
	defer instance.Close()
	if err := platform.InitializeDesktop(dir); err != nil {
		fatal("desktop initialization: ", err)
	}
	defer platform.CloseDesktop()
	// We are the only instance, so a STOP file is left over from an earlier run
	// (e.g. Stop.cmd after a crash). Without this the app would quit immediately.
	_ = os.Remove(filepath.Join(dir, "STOP"))
	// Likewise a recorded llama-server still running was left by a run that died
	// (macOS only; Windows and Linux stop it with the app).
	platform.StopLeftoverServer(platform.ChildRecordPath(dir, conf.Port))

	conf, ok := ensureInstalled(dir, conf)
	if !ok {
		return
	}
	paths, err := config.ResolvePaths(dir, conf)
	if err != nil {
		fatal(err)
	}
	if err := platform.CheckServerPort(conf.Port); err != nil {
		fatal(err)
	}

	serverLog, err := os.OpenFile(filepath.Join(dir, "server.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		fatal(err)
	}
	defer serverLog.Close()

	// The tray exists from here on so the user sees the app while the model loads.
	tray := platform.NewTray(logPath)
	defer tray.Close()
	newApp(dir, conf, paths, tray, serverLog).run()
}

// applyTranslation never replaces unrelated clipboard contents.
func applyTranslation(cb platform.Clipboard, original, replacement string) (bool, error) {
	if replacement == original {
		return false, nil
	}
	return cb.ReplaceIfUnchanged(original, replacement)
}

// formatBilingual joins the source and translation. If both texts contain
// multiple matching paragraphs, they are interleaved paragraph-by-paragraph.
// Otherwise it joins source and translation with a newline, trimming trailing CR/LF.
func formatBilingual(source, translated string) string {
	return textstruct.FormatBilingual(source, translated)
}
