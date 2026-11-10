package app

import (
	"os"
	"strings"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/models"
)

func TestModelDeleteWindowsLockedFile(t *testing.T) {
	f := newSettingsFixture(t)
	s := f.server(t)
	e, _ := models.ByKey("2b")
	putModel(t, f.a.dir, e)

	// Opening with os.Open holds a handle without FILE_SHARE_DELETE,
	// which prevents os.Remove from deleting it on Windows.
	handle, err := os.Open(e.Path(f.a.dir))
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()

	resp, body := s.do(t, "POST", "/api/model/delete", map[string]string{"key": "2b", "kind": "model"}, nil)
	r := decodeAPI(t, body)
	if resp.StatusCode != 422 {
		handle.Close()
		t.Fatalf("locked file should be refused: status %d body %s", resp.StatusCode, body)
	}

	// Must report actionable error without leaking absolute paths.
	if strings.Contains(r.Error, f.a.dir) {
		t.Errorf("error leaked absolute directory %q: %s", f.a.dir, r.Error)
	}
	if !strings.Contains(r.Error, "could not remove models/") {
		t.Errorf("error should name relative file: %s", r.Error)
	}

	// Close the handle so deletion can succeed.
	handle.Close()

	resp2, body2 := s.do(t, "POST", "/api/model/delete", map[string]string{"key": "2b", "kind": "model"}, nil)
	r2 := decodeAPI(t, body2)
	if resp2.StatusCode != 200 || !strings.HasPrefix(r2.Notice, "Deleted 2B (") {
		t.Fatalf("unlocked file deletion should succeed: status %d body %s", resp2.StatusCode, body2)
	}
}
