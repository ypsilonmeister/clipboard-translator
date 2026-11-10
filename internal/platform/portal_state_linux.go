package platform

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Atomic replacement prevents either a torn credential or a partially written desktop entry.
func portalWriteFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".ct-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func installPortalDesktopEntry(dataHome, exe string) error {
	return installPortalDesktopEntryForID(dataHome, exe, waylandAppID)
}

func installPortalDesktopEntryForID(dataHome, exe, id string) error {
	if !filepath.IsAbs(dataHome) {
		return errors.New("XDG_DATA_HOME must be an absolute path")
	}
	entry, err := linuxDesktopEntry(exe)
	if err != nil {
		return err
	}
	dir := filepath.Join(dataHome, "applications")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(dir, id+".desktop")
	want := []byte(entry + "NoDisplay=true\n")
	if got, err := os.ReadFile(path); err == nil && string(got) == string(want) {
		return nil
	}
	return portalWriteFile(path, want)
}

type portalTokenStore struct{ dir string }

func newPortalTokenStore(appDir string) (portalTokenStore, error) {
	s := portalTokenStore{filepath.Join(appDir, "wayland")}
	if !filepath.IsAbs(appDir) {
		return s, errors.New("the portal state directory must be absolute")
	}
	if err := os.Mkdir(s.dir, 0700); err != nil && !os.IsExist(err) {
		return s, err
	}
	st, err := os.Lstat(s.dir)
	if err != nil {
		return s, err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm() != 0700 {
		return s, errors.New("the Wayland state folder must be a private directory (mode 0700)")
	}
	return s, nil
}

func (s portalTokenStore) Load() (string, error) {
	path := filepath.Join(s.dir, "restore-token")
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", errors.New("could not open the private Wayland restore token")
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
		return "", errors.New("the Wayland restore token must be a regular file with mode 0600")
	}
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(b) > 4096 || strings.IndexByte(string(b), 0) >= 0 {
		return "", errors.New("could not read the Wayland restore token")
	}
	return string(b), nil
}

func (s portalTokenStore) Save(token string) error {
	if len(token) > 4096 || strings.IndexByte(token, 0) >= 0 {
		return errors.New("invalid Wayland restore token")
	}
	if token == "" {
		err := os.Remove(filepath.Join(s.dir, "restore-token"))
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return portalWriteFile(filepath.Join(s.dir, "restore-token"), []byte(token))
}
