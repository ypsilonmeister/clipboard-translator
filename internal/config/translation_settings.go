package config

import (
	"bytes"
	"encoding/json"
)

// UpdateTranslationSettings preserves every byte outside the edited values.
// In particular a manual-mode save must not normalize or discard hotkey data.
func UpdateTranslationSettings(dir string, p Profile, languageChanged bool, globals map[string]any) error {
	return updateFile(dir, nil, func(raw []byte) ([]byte, error) {
		var root map[string]json.RawMessage
		if err := json.Unmarshal(raw, &root); err != nil {
			return nil, err
		}
		set := map[string]json.RawMessage{}
		for k, v := range globals {
			b, err := json.Marshal(v)
			if err != nil {
				return nil, err
			}
			set[k] = b
		}
		if languageChanged {
			langs, _ := json.Marshal(p.Languages)
			target, _ := json.Marshal(p.Target)
			fields := map[string]json.RawMessage{}
			if len(p.Languages) > 0 {
				fields["languages"] = langs
			}
			if p.Target != "" {
				fields["target"] = target
			}
			var profilesList []json.RawMessage
			if profiles, ok := root["profiles"]; ok {
				if err := json.Unmarshal(profiles, &profilesList); err != nil {
					return nil, err
				}
			}
			if len(profilesList) > 0 {
				profiles := root["profiles"]
				d := json.NewDecoder(bytes.NewReader(profiles))
				if _, err := d.Token(); err != nil {
					return nil, err
				}
				var first json.RawMessage
				if err := d.Decode(&first); err != nil {
					return nil, err
				}
				end := int(d.InputOffset())
				start := end - len(first)
				var existing map[string]json.RawMessage
				if err := json.Unmarshal(first, &existing); err != nil {
					return nil, err
				}
				if _, ok := existing["target"]; ok {
					fields["target"] = target
				}
				if _, ok := existing["languages"]; ok {
					fields["languages"] = langs
				}
				updated, err := patchObject(first, fields)
				if err != nil {
					return nil, err
				}
				set["profiles"] = append(append(append([]byte{}, profiles[:start]...), updated...), profiles[end:]...)
			} else {
				// Patch legacy fields when there is no first profile, even for an
				// empty array. Load still rejects explicit empty profile arrays.
				if p.Target != "" {
					profiles, err := json.Marshal([]Profile{p})
					if err != nil {
						return nil, err
					}
					set["profiles"] = profiles
				}
				set["languages"] = langs
			}
		}
		return patchObject(raw, set)
	})
}
