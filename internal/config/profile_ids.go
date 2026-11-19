package config

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
)

// NewProfileID creates an identity outside the legacy positional namespace.
func NewProfileID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", fmt.Errorf("generate shortcut id: %w", err)
	}
	return fmt.Sprintf("profile_%x", id), nil
}

func (c *Config) assignLegacyProfileIDs() {
	used := make(map[string]bool, len(c.Profiles))
	for _, p := range c.Profiles {
		used[p.ID] = true
	}
	for i := range c.Profiles {
		p := &c.Profiles[i]
		if p.ID != "" {
			continue
		}
		legacyID := fmt.Sprintf("profile_%d", i+1)
		p.ID = legacyID
		for suffix := 1; used[p.ID]; suffix++ {
			p.ID = fmt.Sprintf("%s_migrated_%d", legacyID, suffix)
		}
		used[p.ID] = true
		c.profileIDsNeedMigration = true
	}
}

// PersistProfileIDs saves migrated IDs before Wayland registers its shortcuts.
// Legacy top-level settings describe a single profile whose ID is always
// profile_1 until the settings are saved as an explicit profile list.
func (c Config) PersistProfileIDs(dir string) error {
	if !c.profileIDsNeedMigration {
		return nil
	}
	return updateFile(dir, nil, func(raw []byte) ([]byte, error) {
		var file struct {
			Profiles json.RawMessage `json:"profiles"`
		}
		if err := json.Unmarshal(raw, &file); err != nil {
			return nil, err
		}
		if len(file.Profiles) == 0 {
			return nil, fmt.Errorf("profiles changed since loading config.json")
		}
		d := json.NewDecoder(bytes.NewReader(file.Profiles))
		if t, err := d.Token(); err != nil || t != json.Delim('[') {
			return nil, fmt.Errorf("profiles changed since loading config.json")
		}
		var updated []byte
		last := 0
		for i, loaded := range c.Profiles {
			var row json.RawMessage
			if err := d.Decode(&row); err != nil {
				return nil, fmt.Errorf("read profiles[%d]: %w", i, err)
			}
			var p Profile
			if err := json.Unmarshal(row, &p); err != nil {
				return nil, err
			}
			if p.Hotkey != loaded.Hotkey || (p.ID != "" && p.ID != loaded.ID) {
				return nil, fmt.Errorf("profiles[%d] changed since loading config.json", i)
			}
			if p.ID == "" {
				id, _ := json.Marshal(loaded.ID)
				patched, err := patchObject(row, map[string]json.RawMessage{"id": id})
				if err != nil {
					return nil, err
				}
				end := int(d.InputOffset())
				updated = append(updated, file.Profiles[last:end-len(row)]...)
				updated = append(updated, patched...)
				last = end
			}
		}
		if d.More() {
			return nil, fmt.Errorf("profiles changed since loading config.json")
		}
		if last == 0 {
			return raw, nil
		}
		updated = append(updated, file.Profiles[last:]...)
		return patchObject(raw, map[string]json.RawMessage{"profiles": updated})
	})
}
