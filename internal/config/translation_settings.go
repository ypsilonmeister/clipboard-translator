package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

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
			edits = append(edits, edit{start, end, replacement})
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

// UpdateTranslationSettings preserves every byte outside the edited values.
// In particular a manual-mode save must not normalize or discard hotkey data.
func UpdateTranslationSettings(dir string, p Profile, languageChanged bool, globals map[string]any) error {
	path := filepath.Join(dir, "config.json")
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	raw := bytes.TrimPrefix(old, utf8BOM)
	if len(old) == 0 {
		raw = []byte("{}\n")
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return err
	}
	set := map[string]json.RawMessage{}
	for k, v := range globals {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		set[k] = b
	}
	if languageChanged {
		langs, _ := json.Marshal(p.Languages)
		target, _ := json.Marshal(p.Target)
		fields := map[string]json.RawMessage{"languages": langs, "target": target}
		if profiles, ok := root["profiles"]; ok {
			d := json.NewDecoder(bytes.NewReader(profiles))
			if _, err := d.Token(); err != nil {
				return err
			}
			var first json.RawMessage
			if err := d.Decode(&first); err != nil {
				return err
			}
			end := int(d.InputOffset())
			start := end - len(first)
			updated, err := patchObject(first, fields)
			if err != nil {
				return err
			}
			set["profiles"] = append(append(append([]byte{}, profiles[:start]...), updated...), profiles[end:]...)
		} else {
			// Legacy configuration has only the top-level language pair.
			if p.Target != "" {
				profiles, err := json.Marshal([]Profile{p})
				if err != nil {
					return err
				}
				set["profiles"] = profiles
			}
			set["languages"] = langs
		}
	}
	updated, err := patchObject(raw, set)
	if err != nil {
		return err
	}
	if bytes.HasPrefix(old, utf8BOM) {
		updated = append(append([]byte{}, utf8BOM...), updated...)
	}
	if len(old) > 0 {
		if err := os.WriteFile(path+".bak", old, 0600); err != nil {
			return err
		}
	}
	return os.WriteFile(path, updated, 0644)
}
