package main

import (
	_ "embed"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

// embeddedSetup is the setup.ps1 of this version, carried inside the exe so a
// bare exe (for example one installed by winget, which does not copy the other
// files of the zip) can still download the model and llama.cpp.
//
//go:embed setup.ps1
var embeddedSetup []byte

//go:embed setup.sh
var embeddedLinuxSetup []byte

func setupContents() []byte {
	if runtime.GOOS == "darwin" {
		return embeddedMacSetup
	}
	if platform.SetupScriptName() == "setup.sh" {
		return embeddedLinuxSetup
	}
	return embeddedSetup
}

func setupProgressDescription() string {
	if runtime.GOOS == "darwin" {
		return "over 1 GB; setup may take several minutes. Progress is written to setup.log in the app data folder"
	}
	if runtime.GOOS == "windows" {
		return "over 1 GB; a setup window opens and closes by itself when it is done"
	}
	return "over 1 GB; setup progress is displayed in the terminal"
}

//go:embed setup-macos.sh
var embeddedMacSetup []byte

// ensureSetupScript makes sure setup.ps1 exists in dir, writing the embedded
// copy when it is missing. An existing file (possibly edited by the user) is
// left alone.
func ensureSetupScript(dir string) error {
	script := filepath.Join(dir, platform.SetupScriptName())
	if err := config.CheckFile(script); err == nil {
		return nil
	}
	return os.WriteFile(script, setupContents(), 0o644)
}

// ensureInstalled makes sure the model and llama-server exist. When they do
// not, it offers to run setup.ps1 (the one next to the exe, or the copy carried
// inside the exe) in a visible window instead of just failing. It returns the config again
// because setup.ps1 rewrites config.json, and false when the user declined.
func ensureInstalled(dir string, conf config.Config) (config.Config, bool) {
	for attempt := 0; ; attempt++ {
		p, err := config.ResolvePaths(dir, conf)
		if err != nil {
			fatal(err)
		}
		missing := p.MissingFor(conf, dir)
		if len(missing) == 0 {
			return conf, true
		}
		list := strings.Join(missing, "\n")
		if attempt > 0 {
			fatal("required file missing:\n", list, "\nRun ", platform.SetupScriptName(), " first.")
		}
		log.Printf("missing files: %v", missing)
		if !platform.AskYesNo("Clipboard Translator needs the PLaMo translation model and llama.cpp, which are not installed yet:\n\n" +
			list + "\n\nDownload them now? (" + setupProgressDescription() + ")") {
			log.Print("setup declined; quitting")
			return conf, false
		}
		// Only now, so declining leaves the folder untouched.
		if err := ensureSetupScript(dir); err != nil {
			fatal("required file missing:\n", list, "\n", platform.SetupScriptName(), " could not be written to the app folder: ", err)
		}
		log.Print("running ", platform.SetupScriptName())
		if err := platform.RunSetup(dir); err != nil {
			fatal("Setup failed: ", err, "\nRun ", platform.SetupScriptName(), " to see the details.")
		}
		if conf, err = config.Load(dir); err != nil {
			fatal("config: ", err)
		}
	}
}
