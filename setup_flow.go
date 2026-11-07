package main

import (
	_ "embed"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	if setupScriptName() == "setup.sh" {
		return embeddedLinuxSetup
	}
	return embeddedSetup
}

func setupProgressDescription() string {
	if runtime.GOOS == "darwin" {
		return "over 1 GB; setup may take several minutes. Progress is written to setup.log in the app data folder"
	}
	return "over 1 GB; run setup.sh in a terminal to see progress (Windows opens a setup window)"
}

//go:embed setup-macos.sh
var embeddedMacSetup []byte

// ensureSetupScript makes sure setup.ps1 exists in dir, writing the embedded
// copy when it is missing. An existing file (possibly edited by the user) is
// left alone.
func ensureSetupScript(dir string) error {
	script := filepath.Join(dir, setupScriptName())
	if _, err := os.Stat(script); err == nil {
		return nil
	}
	return os.WriteFile(script, setupContents(), 0o644)
}

// appPaths holds the absolute paths the config points at.
type appPaths struct{ model, template, server, fallback string }

func resolvePaths(dir string, c Config) (appPaths, error) {
	var p appPaths
	var err error
	if p.model, err = pathWithin(dir, c.ModelFile); err != nil {
		return p, err
	}
	if p.template, err = pathWithin(dir, c.TemplateFile); err != nil {
		return p, err
	}
	if p.server, err = pathWithin(dir, c.ServerFile); err != nil {
		return p, err
	}
	if p.fallback, err = pathWithin(dir, c.FallbackServerFile); err != nil {
		return p, err
	}
	return p, nil
}

// missing lists the files the app cannot start without that do not exist.
func (p appPaths) missing() []string {
	var out []string
	for _, f := range []string{p.model, p.template, p.server} {
		if _, err := os.Stat(f); err != nil {
			out = append(out, f)
		}
	}
	return out
}

// ensureInstalled makes sure the model and llama-server exist. When they do
// not, it offers to run setup.ps1 (the one next to the exe, or the copy carried
// inside the exe) in a visible window instead of just failing. It returns the config again
// because setup.ps1 rewrites config.json, and false when the user declined.
func ensureInstalled(dir string, conf Config) (Config, bool) {
	for attempt := 0; ; attempt++ {
		p, err := resolvePaths(dir, conf)
		if err != nil {
			fatal(err)
		}
		missing := p.missing()
		if len(missing) == 0 {
			return conf, true
		}
		list := strings.Join(missing, "\n")
		if attempt > 0 {
			fatal("required file missing:\n", list, "\nRun ", setupScriptName(), " first.")
		}
		log.Printf("missing files: %v", missing)
		if !askYesNo("Clipboard Translator needs the PLaMo translation model and llama.cpp, which are not installed yet:\n\n" +
			list + "\n\nDownload them now? (" + setupProgressDescription() + ")") {
			log.Print("setup declined; quitting")
			return conf, false
		}
		// Only now, so declining leaves the folder untouched.
		if err := ensureSetupScript(dir); err != nil {
			fatal("required file missing:\n", list, "\n", setupScriptName(), " could not be written to the app folder: ", err)
		}
		log.Print("running ", setupScriptName())
		if err := runSetup(dir); err != nil {
			fatal("Setup failed: ", err, "\nRun ", setupScriptName(), " to see the details.")
		}
		if conf, err = loadConfig(dir); err != nil {
			fatal("config: ", err)
		}
	}
}
