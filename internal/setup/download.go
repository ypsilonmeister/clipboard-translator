package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// download fetches url into dest through dest+".partial" and gives it its
// final name only after its SHA-256 matches want (an empty want skips the
// check, as in the scripts). A failed download leaves neither the final
// file nor a stale partial behind, so a later run cannot mistake either
// for an installed file.
func (r *runner) download(url, dest, want string) error {
	partial := dest + ".partial"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "ClipboardTranslatorSetup")
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the server answered HTTP %d for %s", resp.StatusCode, url)
	}
	out, err := os.Create(partial)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(out, h), resp.Body)
	closeErr := out.Close()
	if copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		_ = os.Remove(partial)
		return fmt.Errorf("download of %s failed: %w", url, copyErr)
	}
	info, err := os.Stat(partial)
	if err != nil {
		_ = os.Remove(partial)
		return err
	}
	if info.Size() == 0 {
		_ = os.Remove(partial)
		return fmt.Errorf("empty download: %s", url)
	}
	if want != "" {
		if got := hex.EncodeToString(h.Sum(nil)); got != want {
			_ = os.Remove(partial)
			return fmt.Errorf("SHA-256 mismatch: %s", filepath.Base(dest))
		}
	}
	if err := os.Rename(partial, dest); err != nil {
		_ = os.Remove(partial)
		return err
	}
	return nil
}
