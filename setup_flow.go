package main

import (
	_ "embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ypsilonmeister/clipboard-translator/internal/models"
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
	if err := checkFile(script); err == nil {
		return nil
	}
	return os.WriteFile(script, setupContents(), 0o644)
}

// appPaths holds the absolute paths the config points at.
type appPaths struct{ model, template, server, fallback string }

func resolvePaths(dir string, c Config) (appPaths, error) {
	var p appPaths
	var err error
	// The key name tells the user which line of config.json to fix.
	resolve := func(key, value string) (string, error) {
		full, err := pathWithin(dir, value)
		if err != nil {
			return "", fmt.Errorf("config.json %q: %w", key, err)
		}
		return full, nil
	}
	if p.model, err = resolve("model_file", c.ModelFile); err != nil {
		return p, err
	}
	if p.template, err = resolve("template_file", c.TemplateFile); err != nil {
		return p, err
	}
	if !models.IsCustomTemplate(c.TemplateFile) {
		if m, ok := models.ForFile(c.ModelFile); ok {
			if tplPath, ok := m.InstalledTemplatePath(dir); ok {
				p.template = tplPath
			}
		}
	}
	if p.server, err = resolve("server_file", c.ServerFile); err != nil {
		return p, err
	}
	if p.fallback, err = resolve("fallback_server_file", c.FallbackServerFile); err != nil {
		return p, err
	}
	return p, nil
}

// checkFile reports why path cannot be used as a required file: it does not
// exist, or it is a folder (or another non-regular entry) instead of a file.
func checkFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a file (it is a folder or special entry)", path)
	}
	return nil
}

// missing lists the files the app cannot start without that do not exist or
// are not regular files.
func (p appPaths) missing() []string {
	var out []string
	for _, f := range []string{p.model, p.template, p.server} {
		if checkFile(f) != nil {
			out = append(out, f)
		}
	}
	return out
}

func (p appPaths) missingFor(c Config, dir string) []string {
	var out []string
	if checkFile(p.model) != nil {
		out = append(out, p.model)
	}
	if models.IsCustomTemplate(c.TemplateFile) {
		if checkFile(p.template) != nil {
			out = append(out, p.template)
		}
	} else {
		m, known := models.ForFile(c.ModelFile)
		if !known || !m.TemplateInstalled(dir) {
			out = append(out, p.template)
		}
	}
	if checkFile(p.server) != nil {
		out = append(out, p.server)
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
		missing := p.missingFor(conf, dir)
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
		if conf, err = loadConfig(dir); err != nil {
			fatal("config: ", err)
		}
	}
}
