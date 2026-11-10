//go:build darwin

package setup

import (
	"os"
	"path/filepath"
	"runtime"
)

func currentPlatform() (platformSpec, error) {
	return platformSpec{
		defaultBackend:    "metal",
		backends:          []string{"cpu", "metal"},
		backendComplete:   unixBackendComplete,
		installRuntimes:   installDarwinRuntimes,
		keptEngineMessage: func(r *runner) {},
		finish: func(r *runner, backend, serverFile string) {
			r.printf("Setup complete. Restart Clipboard Translator.")
			r.printf("Built with PLaMo: https://plamo.preferredai.jp/info/plamo-community-license-ja")
		},
	}, nil
}

func installDarwinRuntimes(r *runner, backend string) (string, error) {
	var archKey string
	switch runtime.GOARCH {
	case "arm64":
		archKey = "arm64"
	case "amd64":
		archKey = "x64"
	default:
		return "", errf("supported macOS architectures: Apple Silicon and Intel x86_64")
	}
	// The official macOS binary serves both CPU (-ngl 0) and Metal; it is
	// installed once, as runtime/cpu, and runtime/metal aliases it.
	if !unixBackendComplete(r.root, "cpu") {
		if err := installTarBackend(r, "cpu", "darwin/"+archKey); err != nil {
			return "", err
		}
	}
	metal := filepath.Join(r.root, "runtime", "metal")
	if _, err := os.Lstat(metal); os.IsNotExist(err) {
		if err := os.Symlink("cpu", metal); err != nil {
			return "", err
		}
	}
	if !unixBackendComplete(r.root, "metal") {
		return "", errf("metal runtime alias is incomplete; move runtime/metal aside and retry")
	}
	return backend, nil
}
