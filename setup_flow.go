package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

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
// not and setup.ps1 is next to the exe (portable zip, Scoop), it offers to run
// it in a visible window instead of just failing. It returns the config again
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
		script := filepath.Join(dir, "setup.ps1")
		if _, statErr := os.Stat(script); statErr != nil || attempt > 0 {
			fatal("required file missing:\n", list, "\nRun Setup.cmd (or setup.ps1) first.")
		}
		log.Printf("missing files: %v", missing)
		if !askYesNo("Clipboard Translator needs the PLaMo translation model and llama.cpp, which are not installed yet:\n\n" +
			list + "\n\nDownload them now? (over 1 GB; a setup window opens and closes by itself when it is done)") {
			log.Print("setup declined; quitting")
			return conf, false
		}
		log.Print("running setup.ps1")
		if err := runSetup(dir); err != nil {
			fatal("Setup failed: ", err, "\nRun Setup.cmd to see the details.")
		}
		if conf, err = loadConfig(dir); err != nil {
			fatal("config: ", err)
		}
	}
}
