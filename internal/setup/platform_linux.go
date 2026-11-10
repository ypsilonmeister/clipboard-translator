//go:build linux

package setup

import (
	"runtime"
)

func currentPlatform() (platformSpec, error) {
	return platformSpec{
		defaultBackend:    "cpu",
		backends:          []string{"cpu", "vulkan"},
		backendComplete:   unixBackendComplete,
		installRuntimes:   installLinuxRuntimes,
		keptEngineMessage: func(r *runner) {},
		finish: func(r *runner, backend, serverFile string) {
			r.printf("Setup complete. Start ./clipboard-translator in an X11 session.")
			r.printf("Built with PLaMo: https://plamo.preferredai.jp/info/plamo-community-license-ja")
		},
	}, nil
}

func installLinuxRuntimes(r *runner, backend string) (string, error) {
	switch runtime.GOARCH {
	case "amd64":
	case "arm64":
		return "", errf("pre-built Linux binaries are currently pinned for x86_64")
	default:
		return "", errf("supported Linux architectures: x86_64 and aarch64")
	}
	needed := []string{}
	seen := map[string]bool{}
	for _, b := range []string{"cpu", backend} {
		if seen[b] {
			continue
		}
		seen[b] = true
		if !unixBackendComplete(r.root, b) {
			needed = append(needed, b)
		}
	}
	if len(needed) > 0 {
		r.printf("Using official llama.cpp release %s", llamaTag)
	}
	for _, b := range needed {
		if err := installTarBackend(r, b, "linux/"+b); err != nil {
			return "", err
		}
	}
	return backend, nil
}
