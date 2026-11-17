package app

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenTranslatorLogRotation(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int
	}{
		{"missing", -1},
		{"small", 32},
		{"at limit", translatorLogLimit},
		{"oversized", translatorLogLimit + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "translator.log")
			old := bytes.Repeat([]byte("x"), max(0, tc.size))
			if tc.size >= 0 {
				if err := os.WriteFile(path, old, 0600); err != nil {
					t.Fatal(err)
				}
			}
			const previous = "previous rotation"
			if err := os.WriteFile(path+".1", []byte(previous), 0600); err != nil {
				t.Fatal(err)
			}
			f, err := openTranslatorLog(path)
			if err != nil {
				t.Fatal(err)
			}
			_, writeErr := f.WriteString("new entry\n")
			closeErr := f.Close()
			if writeErr != nil || closeErr != nil {
				t.Fatalf("write=%v close=%v", writeErr, closeErr)
			}
			wantCurrent, wantRotated := string(old)+"new entry\n", previous
			if tc.size > translatorLogLimit {
				wantCurrent, wantRotated = "new entry\n", string(old)
			}
			for name, want := range map[string]string{path: wantCurrent, path + ".1": wantRotated} {
				got, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != want {
					t.Fatalf("%s: content differs (got %d bytes, want %d)", filepath.Base(name), len(got), len(want))
				}
			}
		})
	}
}
