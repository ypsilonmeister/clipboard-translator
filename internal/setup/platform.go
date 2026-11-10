package setup

import (
	"fmt"
	"os"
	"path/filepath"
)

// platformSpec carries everything that differs between the three setup
// scripts this package replaces: the backend names, how a backend is
// judged complete, how the runtimes are installed, and the final messages.
type platformSpec struct {
	autoDetect      bool   // Windows: "" / "auto" resolves through GPU detection
	modelBannerSize bool   // setup.ps1 prints the model size in its banner
	defaultBackend  string // used when --backend is not given (never explicit)
	backends        []string

	backendComplete   func(root, backend string) bool
	detectBackend     func(r *runner) string
	installRuntimes   func(r *runner, backend string) (string, error)
	keptEngineMessage func(r *runner)
	finish            func(r *runner, backend, serverFile string)
}

func (p platformSpec) validBackend(b string) bool {
	if p.autoDetect && b == "auto" {
		return true
	}
	for _, name := range p.backends {
		if b == name {
			return true
		}
	}
	return false
}

// unixBackendComplete is the Linux/macOS rule: the backend's llama-server
// file exists (for the macOS metal alias, through its symlink).
func unixBackendComplete(root, backend string) bool {
	info, err := os.Stat(serverFilePath(root, backend))
	return err == nil && info.Mode().IsRegular()
}

// installTarBackend downloads one pinned tar.gz asset, unpacks it in a
// staging folder under runtime/ and renames the llama-server's folder
// into runtime/<name>. A partial download or extraction never becomes
// visible as an installed backend, and an existing runtime/<name> (which
// can only be an incomplete leftover, a complete one is skipped by the
// caller) is refused rather than overwritten — the Unix scripts' rule.
func installTarBackend(r *runner, name, assetKey string) error {
	asset, err := llamaAssetFor(assetKey)
	if err != nil {
		return err
	}
	if !sha256Pattern.MatchString(asset.Sha256) {
		return errf("missing SHA-256 for %s", asset.File)
	}
	runtimeDir := filepath.Join(r.root, "runtime")
	tmp, err := os.MkdirTemp(runtimeDir, ".setup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	archive := filepath.Join(tmp, "llama.tar.gz")
	if err := r.download(asset.url(), archive, asset.Sha256); err != nil {
		return err
	}
	unpacked := filepath.Join(tmp, "unpacked")
	if err := extractTarGz(archive, unpacked); err != nil {
		return err
	}
	dirs := findServerDirs(unpacked, "llama-server")
	if len(dirs) != 1 {
		return errf("expected one llama-server executable in %s", asset.File)
	}
	server := filepath.Join(dirs[0], "llama-server")
	if info, err := os.Stat(server); err != nil {
		return err
	} else if err := os.Chmod(server, info.Mode()|0o111); err != nil {
		return err
	}
	target := filepath.Join(runtimeDir, name)
	if _, err := os.Lstat(target); err == nil {
		return errf("incomplete runtime directory exists; move it aside and retry: %s", target)
	}
	return os.Rename(dirs[0], target)
}

type setupError string

func (e setupError) Error() string { return string(e) }

func errf(format string, args ...any) error {
	return setupError(fmt.Sprintf(format, args...))
}
