package config

import (
	"fmt"
	"os"

	"github.com/ypsilonmeister/clipboard-translator/internal/models"
)

// Paths holds the absolute paths the config points at.
type Paths struct{ Model, Template, Server, Fallback string }

func ResolvePaths(dir string, c Config) (Paths, error) {
	var p Paths
	var err error
	// The key name tells the user which line of config.json to fix.
	resolve := func(key, value string) (string, error) {
		full, err := PathWithin(dir, value)
		if err != nil {
			return "", fmt.Errorf("config.json %q: %w", key, err)
		}
		return full, nil
	}
	if p.Model, err = resolve("model_file", c.ModelFile); err != nil {
		return p, err
	}
	if p.Template, err = resolve("template_file", c.TemplateFile); err != nil {
		return p, err
	}
	if !models.IsCustomTemplate(c.TemplateFile) {
		if m, ok := models.ForFile(c.ModelFile); ok {
			if tplPath, ok := m.InstalledTemplatePath(dir); ok {
				p.Template = tplPath
			}
		}
	}
	if p.Server, err = resolve("server_file", c.ServerFile); err != nil {
		return p, err
	}
	if p.Fallback, err = resolve("fallback_server_file", c.FallbackServerFile); err != nil {
		return p, err
	}
	return p, nil
}

// CheckFile reports why path cannot be used as a required file: it does not
// exist, or it is a folder (or another non-regular entry) instead of a file.
func CheckFile(path string) error {
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
func (p Paths) Missing() []string {
	var out []string
	for _, f := range []string{p.Model, p.Template, p.Server} {
		if CheckFile(f) != nil {
			out = append(out, f)
		}
	}
	return out
}

func (p Paths) MissingFor(c Config, dir string) []string {
	var out []string
	if CheckFile(p.Model) != nil {
		out = append(out, p.Model)
	}
	if models.IsCustomTemplate(c.TemplateFile) {
		if CheckFile(p.Template) != nil {
			out = append(out, p.Template)
		}
	} else {
		m, known := models.ForFile(c.ModelFile)
		if !known || !m.TemplateInstalled(dir) {
			out = append(out, p.Template)
		}
	}
	if CheckFile(p.Server) != nil {
		out = append(out, p.Server)
	}
	return out
}
