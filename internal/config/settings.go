package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

// BackendNames are the llama.cpp builds setup.ps1 can install, in Settings order.
var BackendNames = []string{"cpu", "cuda", "vulkan", "metal"}

// AvailableBackends lists the installed builds (runtime/<name>/llama-server.exe).
func AvailableBackends(dir string) []string {
	var out []string
	for _, b := range BackendNames {
		if err := CheckFile(filepath.Join(dir, "runtime", b, platform.ServerBinaryName())); err == nil {
			out = append(out, b)
		}
	}
	return out
}

// CurrentBackend derives the backend from conf.ServerFile ("runtime/<name>/...").
func CurrentBackend(serverFile string) string {
	// config.json may use either slash; filepath.ToSlash would leave backslashes alone off Windows.
	parts := strings.Split(strings.ReplaceAll(serverFile, `\`, "/"), "/")
	if len(parts) >= 3 && parts[0] == "runtime" {
		return parts[1]
	}
	return ""
}

// BackendSettings are the config values setup.ps1 itself writes for a backend.
func BackendSettings(name string) (serverFile string, gpuLayers int) {
	gpuLayers = 99
	if name == "cpu" {
		gpuLayers = 0
	}
	return "runtime/" + name + "/" + platform.ServerBinaryName(), gpuLayers
}

type configEntry struct {
	key string
	val json.RawMessage
}

// parseConfigEntries splits a config.json object into its entries, in file
// order. A leading UTF-8 BOM is ignored. A key that appears twice becomes one
// entry at its first position with the last value, as json.Unmarshal (and so
// Load) reads it. Anything after the closing brace is an error.
func parseConfigEntries(b []byte) ([]configEntry, error) {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(b, utf8BOM)))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, errors.New("config.json must be a JSON object")
	}
	var out []configEntry
	index := map[string]int{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		key := kt.(string)
		if i, dup := index[key]; dup {
			out[i].val = raw
			continue
		}
		index[key] = len(out)
		out = append(out, configEntry{key, raw})
	}
	// Consume the closing brace (a truncated file fails here) ...
	if t, err := dec.Token(); err != nil || t != json.Delim('}') {
		if err == nil || errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return nil, fmt.Errorf("config.json is incomplete or not valid JSON: %w", err)
	}
	// ... and make sure nothing but whitespace follows it.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("config.json has extra data after the closing }")
	}
	return out, nil
}

// UpdateFile sets the given keys in config.json. Every other key, known
// to this version or not, keeps its value and its position. The previous file is
// saved as config.json.bak. The file is rewritten in place (not replaced by a
// rename) so a hard link, as used by Scoop's "persist", stays intact. A UTF-8
// BOM at the start of the old file is accepted; the .bak keeps it, the new
// file is written without one.
func UpdateFile(dir string, set map[string]any) error {
	path := filepath.Join(dir, "config.json")
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var entries []configEntry
	if len(old) > 0 {
		if entries, err = parseConfigEntries(old); err != nil {
			return err
		}
		if err := os.WriteFile(path+".bak", old, 0o600); err != nil {
			return err
		}
	}
	// Apply in a stable order: existing keys first (in place), then new ones sorted.
	var added []string
	for k := range set {
		found := false
		for _, e := range entries {
			if e.key == k {
				found = true
			}
		}
		if !found {
			added = append(added, k)
		}
	}
	sort.Strings(added)
	for _, k := range added {
		entries = append(entries, configEntry{key: k})
	}
	var out bytes.Buffer
	out.WriteString("{\n")
	for i, e := range entries {
		raw := e.val
		if v, ok := set[e.key]; ok {
			b, err := json.Marshal(v)
			if err != nil {
				return err
			}
			raw = b
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, raw); err != nil {
			return err
		}
		k, _ := json.Marshal(e.key)
		out.WriteString("  ")
		out.Write(k)
		out.WriteString(": ")
		out.Write(compact.Bytes())
		if i < len(entries)-1 {
			out.WriteString(",")
		}
		out.WriteString("\n")
	}
	out.WriteString("}\n")
	return os.WriteFile(path, out.Bytes(), 0o644)
}
