//go:build windows

package setup

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func currentPlatform() (platformSpec, error) {
	return platformSpec{
		autoDetect:      true,
		modelBannerSize: true,
		defaultBackend:  "auto",
		backends:        []string{"cpu", "cuda", "vulkan"},
		backendComplete: windowsBackendComplete,
		detectBackend:   detectWindowsBackend,
		installRuntimes: installWindowsRuntimes,
		keptEngineMessage: func(r *runner) {
			r.printf("Kept your existing engine settings (server_file, gpu_layers) in config.json.")
		},
		finish: finishWindows,
	}, nil
}

// windowsBackendComplete is Test-BackendComplete from setup.ps1: the
// server exe, and for CUDA also the CUDA runtime DLLs it loads.
func windowsBackendComplete(root, backend string) bool {
	return backendCompleteDir(filepath.Join(root, "runtime", backend), backend)
}

func backendCompleteDir(dir, name string) bool {
	info, err := os.Stat(filepath.Join(dir, "llama-server.exe"))
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	if name == "cuda" {
		for _, pattern := range []string{"cudart64_*.dll", "cublas64_*.dll"} {
			matches, _ := filepath.Glob(filepath.Join(dir, pattern))
			if len(matches) == 0 {
				return false
			}
		}
	}
	return true
}

var (
	nvidiaPattern = regexp.MustCompile(`(?i)NVIDIA|GeForce|RTX|Quadro`)
	amdPattern    = regexp.MustCompile(`(?i)AMD|Radeon|Intel|Arc`)
)

// detectWindowsBackend mirrors setup.ps1 -Backend auto: NVIDIA → cuda,
// AMD/Intel → vulkan, anything else (or a failed query) → cpu.
func detectWindowsBackend(r *runner) string {
	out, err := exec.Command("powershell.exe", "-NoProfile", "-Command",
		"Get-CimInstance Win32_VideoController | ForEach-Object { $_.Name }").Output()
	if err != nil {
		r.printf("Warning: GPU detection failed, using CPU. Run setup again with --backend cuda or --backend vulkan to select manually.")
		return "cpu"
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}
	r.printf("Detected display devices: %s", strings.Join(names, "; "))
	joined := strings.Join(names, "\n")
	switch {
	case nvidiaPattern.MatchString(joined):
		return "cuda"
	case amdPattern.MatchString(joined):
		return "vulkan"
	default:
		return "cpu"
	}
}

func installWindowsRuntimes(r *runner, backend string) (string, error) {
	r.printf("Requested backend: %s", backend)
	needCPU := !windowsBackendComplete(r.root, "cpu")
	needGPU := backend != "cpu" && !windowsBackendComplete(r.root, backend)
	if !needCPU && !needGPU {
		extra := ""
		if backend != "cpu" {
			extra = ", " + backend
		}
		r.printf("llama.cpp is already installed (cpu%s).", extra)
		return backend, nil
	}
	r.printf("Installing pinned llama.cpp release: %s", llamaTag)
	if needCPU {
		if err := installZipBackend(r, "cpu", ""); err != nil {
			return "", err
		}
	}
	installed := backend
	if needGPU {
		dllKey := ""
		if backend == "cuda" {
			dllKey = "win/cudart"
		}
		if err := installZipBackend(r, backend, dllKey); err != nil {
			r.printf("Warning: Could not install %s: %v", backend, err)
			r.printf("Warning: Using CPU fallback instead. Run Setup.cmd later to retry GPU installation.")
			installed = "cpu"
		}
	}
	return installed, nil
}

// installZipBackend installs runtime\<name> all or nothing, like
// Install-Backend in setup.ps1: both zips are downloaded, hash-checked
// and unpacked into staging first; only a complete staging folder is
// renamed into place, replacing an incomplete leftover.
func installZipBackend(r *runner, name, dllAssetKey string) (err error) {
	asset, err := llamaAssetFor("win/" + name)
	if err != nil {
		return err
	}
	if !sha256Pattern.MatchString(asset.Sha256) {
		return errf("missing SHA-256 for %s", asset.File)
	}
	runtimeDir := filepath.Join(r.root, "runtime")
	target := filepath.Join(runtimeDir, name)
	defer func() {
		// Do not leave an incomplete backend behind on failure: the app
		// only checks for llama-server.exe and would offer a broken one.
		if err != nil {
			if _, statErr := os.Stat(target); statErr == nil && !backendCompleteDir(target, name) {
				_ = os.RemoveAll(target)
			}
		}
	}()
	stage, err := os.MkdirTemp(runtimeDir, ".staging-"+name+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	tmp, err := os.MkdirTemp("", "PortableTranslator-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	if err := addZipAsset(r, asset, tmp, stage, false); err != nil {
		return err
	}
	if dllAssetKey != "" {
		dllAsset, err := llamaAssetFor(dllAssetKey)
		if err != nil {
			return err
		}
		if err := addZipAsset(r, dllAsset, tmp, stage, true); err != nil {
			return err
		}
	}
	if !backendCompleteDir(stage, name) {
		return errf("the %s package is incomplete (llama-server.exe or the CUDA runtime DLLs are missing)", name)
	}
	if _, err := os.Lstat(target); err == nil {
		if err := os.RemoveAll(target); err != nil {
			return err
		}
	}
	return os.Rename(stage, target)
}

// addZipAsset downloads one release zip, checks its SHA-256 and copies
// the wanted files flat into stage: the files next to llama-server.exe,
// or (onlyDlls) every DLL in the package.
func addZipAsset(r *runner, asset llamaAsset, tmp, stage string, onlyDlls bool) error {
	id := filepath.Base(tmp) + "-" + asset.File
	zipPath := filepath.Join(tmp, id+".zip")
	if err := r.download(asset.url(), zipPath, asset.Sha256); err != nil {
		return err
	}
	unpacked := filepath.Join(tmp, id)
	if err := extractZip(zipPath, unpacked); err != nil {
		return err
	}
	if onlyDlls {
		found := 0
		err := filepath.WalkDir(unpacked, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type().IsRegular() && strings.EqualFold(filepath.Ext(d.Name()), ".dll") {
				found++
				return copyFile(path, filepath.Join(stage, d.Name()))
			}
			return nil
		})
		if err != nil {
			return err
		}
		if found == 0 {
			return errf("CUDA DLL package contains no DLL files: %s", asset.File)
		}
		return nil
	}
	dirs := findServerDirs(unpacked, "llama-server.exe")
	if len(dirs) == 0 {
		return errf("llama-server.exe not found in %s", asset.File)
	}
	entries, err := os.ReadDir(dirs[0])
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			if err := copyFile(filepath.Join(dirs[0], entry.Name()), filepath.Join(stage, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func finishWindows(r *runner, backend, serverFile string) {
	// llama.cpp's Windows builds import vcruntime140_1.dll; without the
	// Visual C++ runtime llama-server exits at once and prints nothing,
	// so say so now, while the user is looking at this window.
	system := filepath.Join(os.Getenv("SystemRoot"), "System32", "vcruntime140_1.dll")
	if _, err := os.Stat(system); err != nil {
		r.printf("Warning: The Visual C++ Redistributable (x64) looks missing or too old; llama-server will not start without it.")
		r.printf("Warning: Install it with:  winget install --id Microsoft.VCRedist.2015+.x64 -e   (or https://aka.ms/vs/17/release/vc_redist.x64.exe)")
	}
	r.printf("")
	r.printf("Setup finished. Engine in config.json: %s; CPU fallback installed.", serverFile)
	r.printf("Now start ClipboardTranslator.exe (or Start.cmd). The full folder can be copied to another Windows x64 PC.")
	r.printf("A different GPU or driver on the destination may trigger CPU fallback.")
	r.printf("PLaMo terms: https://huggingface.co/pfnet/%s", r.entry.Repo)
}
