package main

import (
	"os"
	"path/filepath"
	"strings"
)

// modelEntry is one model the app can install. The values are pinned to one
// Hugging Face commit and SHA-256 each, exactly as in setup.ps1, setup.sh and
// setup-macos.sh (a test compares them with setup.ps1): to update a model, bump
// Revision, Sha256 and TemplateSha256 together in all of them.
type modelEntry struct {
	Key, Name, Note string
	File, Repo      string
	Revision        string
	Sha256          string
	TemplateSha256  string
	SizeLabel       string
	MinBytes        int64 // a file smaller than this is not a complete model
	Bytes           int64 // expected size, when known (progress if the server sends no length)
}

// Only Q4_K_M is offered, and not the 31B model (about 20 GB).
var modelCatalog = []modelEntry{
	{Key: "0.8b", Name: "PLaMo 3 Translate 0.8B", Note: "Smallest and fastest.",
		File: "plamo-3-translate-0.8b-Q4_K_M.gguf", Repo: "plamo-3-translate-0.8b-GGUF",
		Revision:       "0c4af2b003fdd94b6868aa955414c1c86ee96997",
		Sha256:         "73a1d942d8ab1e654d83898d997b8b4808c32ba669b378df612230989d18f079",
		TemplateSha256: "084af862c7844bdca74e8dca5516fde1f446919e00650ee4d8fae5b193fd064e",
		SizeLabel:      "0.6 GB", MinBytes: 400 << 20, Bytes: 573125952},
	// Revision checked 2026-11-09, outputs identical to the previous pin on 46
	// requests. Upstream template changes: no longer defaults target_lang to ja
	// when omitted and no longer special-cases ja(easy); the app always sends
	// target_lang so it is unaffected.
	{Key: "2b", Name: "PLaMo 3 Translate 2B", Note: "The default.",
		File: "plamo-3-translate-2b-Q4_K_M.gguf", Repo: "plamo-3-translate-2b-GGUF",
		Revision:       "228a27cc0bb4f111219963848d50a3127c16ca73",
		Sha256:         "1ffd9073afcc70e35ccc942428aa2b73d7aa2b9ea779bf504b775369e0276c13",
		TemplateSha256: "ad7eb87b161c6792be070d777a9f9da50c9e57a77e1fa5cca3a24b91b6bdb9e9",
		SizeLabel:      "1.8 GB", MinBytes: 1 << 30, Bytes: 1807203040},
	{Key: "8b", Name: "PLaMo 3 Translate 8B", Note: "The largest on offer: needs more disk space, memory and time.",
		File: "plamo-3-translate-8b-Q4_K_M.gguf", Repo: "plamo-3-translate-8b-GGUF",
		Revision:       "407c15bc44f6968e6734bc047d92f766c163bbec",
		Sha256:         "e021fb83dcda393a4f866291caa53380eebbe2f25be4c7585fb19e8486828bbf",
		TemplateSha256: "084af862c7844bdca74e8dca5516fde1f446919e00650ee4d8fae5b193fd064e",
		SizeLabel:      "5.3 GB", MinBytes: 4 << 30, Bytes: 5280444832},
}

func modelByKey(key string) (modelEntry, bool) {
	for _, m := range modelCatalog {
		if m.Key == key {
			return m, true
		}
	}
	return modelEntry{}, false
}

// modelForFile finds the catalog entry whose file is the one a model_file value
// names (either slash, any case).
func modelForFile(modelFile string) (modelEntry, bool) {
	leaf := modelFile[strings.LastIndexAny(modelFile, `/\`)+1:]
	for _, m := range modelCatalog {
		if strings.EqualFold(m.File, leaf) {
			return m, true
		}
	}
	return modelEntry{}, false
}

func (m modelEntry) path(dir string) string { return filepath.Join(dir, "models", m.File) }

// installed reports whether the model's file is in dir\models and complete
// enough to be one.
func (m modelEntry) installed(dir string) bool {
	info, err := os.Stat(m.path(dir))
	return err == nil && info.Mode().IsRegular() && info.Size() >= m.MinBytes
}

func isRemovableFile(info os.FileInfo) bool {
	return !info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

// partialInfo reports whether a .partial file exists for the model, and its size.
func (m modelEntry) partialInfo(dir string) (bool, int64) {
	info, err := os.Lstat(m.path(dir) + ".partial")
	if err == nil && isRemovableFile(info) {
		return true, info.Size()
	}
	return false, 0
}

// fileInfo reports whether the model file exists, and its size.
func (m modelEntry) fileInfo(dir string) (bool, int64) {
	info, err := os.Lstat(m.path(dir))
	if err == nil && isRemovableFile(info) {
		return true, info.Size()
	}
	return false, 0
}

func templateInstalled(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "models", "chat_template.jinja"))
	return err == nil && info.Mode().IsRegular() && info.Size() >= 100
}
