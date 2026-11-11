package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
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
	return updateFile(dir, set, nil)
}

// updateFile owns validation, backups and writes for all config editors.
// A transform preserves formatting (including a BOM) for nested edits.
func updateFile(dir string, set map[string]any, transform func([]byte) ([]byte, error)) error {
	path := filepath.Join(dir, "config.json")
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	raw := bytes.TrimPrefix(old, utf8BOM)
	if len(raw) == 0 {
		raw = []byte("{}\n")
	}
	if _, err := parseConfigEntries(raw); err != nil {
		return err
	}
	if transform != nil {
		updated, err := transform(raw)
		if err != nil {
			return err
		}
		if bytes.HasPrefix(old, utf8BOM) {
			updated = append(append([]byte{}, utf8BOM...), updated...)
		}
		return writeConfigFile(path, old, updated)
	}
	fields := make(map[string]json.RawMessage, len(set))
	for key, value := range set {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		fields[key] = encoded
	}
	updated, err := patchObject(raw, fields)
	if err != nil {
		return err
	}
	entries, err := parseConfigEntries(updated)
	if err != nil {
		return err
	}
	// Retain UpdateFile's established normalized output; nested translation edits
	// above use the same patch engine while preserving the original formatting.

	var out bytes.Buffer
	out.WriteString("{\n")
	for i, e := range entries {
		raw := e.val
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
	return writeConfigFile(path, old, out.Bytes())
}

// patchObject changes only value spans, retaining unknown fields and formatting.
func patchObject(raw []byte, set map[string]json.RawMessage) ([]byte, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	if t, err := d.Token(); err != nil || t != json.Delim('{') {
		return nil, fmt.Errorf("expected config object")
	}
	type edit struct {
		start, end int
		value      []byte
	}
	var edits []edit
	found := map[string]bool{}
	members := 0
	for d.More() {
		members++
		key, err := d.Token()
		if err != nil {
			return nil, err
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return nil, err
		}
		end := int(d.InputOffset())
		// Decode starts before the colon and whitespace; locate the raw value.
		start := end - len(value)
		if replacement, ok := set[key.(string)]; ok {
			var a, b any
			left := json.NewDecoder(bytes.NewReader(value))
			left.UseNumber()
			right := json.NewDecoder(bytes.NewReader(replacement))
			right.UseNumber()
			if err := left.Decode(&a); err != nil {
				return nil, err
			}
			if err := right.Decode(&b); err != nil {
				return nil, err
			}
			if !reflect.DeepEqual(a, b) {
				edits = append(edits, edit{start, end, replacement})
			}
			found[key.(string)] = true
		}
	}
	closeAt := int(d.InputOffset())
	for closeAt < len(raw) && raw[closeAt] != '}' {
		closeAt++
	}
	var keys []string
	for key := range set {
		if !found[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var extra bytes.Buffer
	for i, key := range keys {
		if members > 0 || i > 0 {
			extra.WriteByte(',')
		}
		k, _ := json.Marshal(key)
		extra.Write(k)
		extra.WriteByte(':')
		extra.Write(set[key])
	}
	if extra.Len() > 0 {
		edits = append(edits, edit{closeAt, closeAt, extra.Bytes()})
	}
	for i := len(edits) - 1; i >= 0; i-- {
		e := edits[i]
		raw = append(append(append([]byte{}, raw[:e.start]...), e.value...), raw[e.end:]...)
	}
	return raw, nil
}

// writeConfigFile keeps Scoop hard links intact and leaves no backup on no-op saves.
func writeConfigFile(path string, old, updated []byte) error {
	if bytes.Equal(old, updated) {
		return nil
	}
	if len(old) > 0 {
		if err := os.WriteFile(path+".bak", old, 0600); err != nil {
			return err
		}
	}
	return os.WriteFile(path, updated, 0644)
}
