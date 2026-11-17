//go:build linux

package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type linuxInstallerFixture struct{ root, dir, bin, assets string }

func writeInstallFixture(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func newLinuxInstallerFixture(t *testing.T) linuxInstallerFixture {
	t.Helper()
	f := linuxInstallerFixture{root: t.TempDir()}
	f.dir = filepath.Join(f.root, "app with spaces")
	f.bin = filepath.Join(f.root, "bin")
	f.assets = filepath.Join(f.root, "assets")
	name := "clipboard-translator-linux-amd64-v1.2.3"
	archive := filepath.Join(f.assets, name+".tar.gz")
	if err := os.MkdirAll(f.assets, 0755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	for _, entry := range []struct {
		name, body string
		mode       int64
	}{
		{"clipboard-translator", "#!/bin/sh\nprintf launched > \"$(dirname \"$0\")/launched\"\n", 0755},
		{"setup.sh", "#!/bin/bash\nprintf setup > \"$(dirname \"$0\")/setup-ran\"\n", 0755},
		{"config.json", "original config", 0644}, {"LICENSE", "license", 0644},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: name + "/" + entry.name, Mode: entry.mode, Size: int64(len(entry.body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	for _, err := range []error{tw.Close(), gz.Close(), file.Close()} {
		if err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	writeInstallFixture(t, archive+".sha256", fmt.Sprintf("%x  %s.tar.gz\n", sha256.Sum256(b), name), 0644)
	// Stub only the network boundary; exercise the real shell, tar and hashing.
	writeInstallFixture(t, filepath.Join(f.bin, "curl"), `#!/bin/sh
while [ "$#" -gt 0 ]; do
 case $1 in -o) dest=$2; shift 2 ;; -*) shift ;; *) url=$1; shift ;; esac
done
case $url in
 */releases/latest) printf '{"tag_name":"v1.2.3"}' > "$dest" ;;
 *) cp "$FIXTURE_ASSETS/${url##*/}" "$dest" ;;
esac
`, 0755)
	return f
}
func (f linuxInstallerFixture) run(t *testing.T, env []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("sh", append([]string{"install.sh"}, args...)...)
	cmd.Env = append(os.Environ(), "HOME="+f.root, "XDG_DATA_HOME="+filepath.Join(f.root, "data"), "XDG_CONFIG_HOME="+filepath.Join(f.root, "config"), "CT_INSTALL_DIR="+f.dir, "CT_VERSION=", "CT_SKIP_SETUP=1", "CT_NO_LAUNCH=1", "CT_UNINSTALL=0", "CT_YES=0", "XDG_SESSION_TYPE=", "DISPLAY=", "WAYLAND_DISPLAY=", "PATH="+f.bin+":"+os.Getenv("PATH"), "FIXTURE_ASSETS="+f.assets)
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
func TestLinuxInstallUpdateAndUninstall(t *testing.T) {
	f := newLinuxInstallerFixture(t)
	if out, err := f.run(t, nil); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	for _, name := range []string{"clipboard-translator", "setup.sh", "config.json", ".installed"} {
		if _, err := os.Stat(filepath.Join(f.dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	desktop := filepath.Join(f.root, "data/applications/clipboard-translator.desktop")
	b, err := os.ReadFile(desktop)
	if err != nil || !strings.Contains(string(b), `Exec="`+f.dir+`/clipboard-translator"`) {
		t.Fatalf("desktop: %s %v", b, err)
	}
	for _, name := range []string{"config.json", "models/other.gguf", "models/plamo-3-translate-2b-Q4_K_M.gguf", "runtime/cpu/llama-server", "sentinel"} {
		writeInstallFixture(t, filepath.Join(f.dir, name), "keep", 0644)
	}
	if out, err := f.run(t, nil, "--version", "v1.2.3"); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	for _, name := range []string{"config.json", "models/plamo-3-translate-2b-Q4_K_M.gguf", "runtime/cpu/llama-server"} {
		b, err := os.ReadFile(filepath.Join(f.dir, name))
		if err != nil || string(b) != "keep" {
			t.Fatalf("lost %s: %q %v", name, b, err)
		}
	}
	// A registration for another installation must survive uninstall.
	writeInstallFixture(t, desktop, "Exec=\"/other/clipboard-translator\"\n", 0644)
	if out, err := f.run(t, nil, "--uninstall", "--yes"); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out)
	}
	for _, name := range []string{"clipboard-translator", "config.json", "models/plamo-3-translate-2b-Q4_K_M.gguf", "runtime", ".installed"} {
		if _, err := os.Stat(filepath.Join(f.dir, name)); !os.IsNotExist(err) {
			t.Errorf("not removed: %s", name)
		}
	}
	for _, name := range []string{"models/other.gguf", "sentinel"} {
		if _, err := os.Stat(filepath.Join(f.dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(desktop); err != nil {
		t.Fatal(err)
	}
}
func TestLinuxInstallChecksumBeforeWrites(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			f := newLinuxInstallerFixture(t)
			if existing {
				writeInstallFixture(t, filepath.Join(f.dir, "clipboard-translator"), "old app", 0755)
				writeInstallFixture(t, filepath.Join(f.dir, "STOP"), "unchanged", 0644)
			}
			writeInstallFixture(t, filepath.Join(f.assets, "clipboard-translator-linux-amd64-v1.2.3.tar.gz.sha256"), strings.Repeat("0", 64), 0644)
			out, err := f.run(t, nil)
			if err == nil || !strings.Contains(out, "SHA-256 mismatch") {
				t.Fatalf("%v %s", err, out)
			}
			if !existing {
				if _, err := os.Stat(f.dir); !os.IsNotExist(err) {
					t.Fatal("created install dir")
				}
			} else {
				b, _ := os.ReadFile(filepath.Join(f.dir, "STOP"))
				if string(b) != "unchanged" {
					t.Fatal("modified install dir")
				}
			}
		})
	}
}
func TestLinuxInstallRejectsForeignAndUnsafeDirectories(t *testing.T) {
	f := newLinuxInstallerFixture(t)
	writeInstallFixture(t, filepath.Join(f.dir, "sentinel"), "keep", 0644)
	if out, err := f.run(t, nil); err == nil || !strings.Contains(out, "is not empty") {
		t.Fatalf("%v %s", err, out)
	}
	for _, dir := range []string{"/", f.root, filepath.Join(f.root, "data")} {
		if out, err := f.run(t, nil, "--install-dir", dir); err == nil || !strings.Contains(out, "Unsafe install directory") {
			t.Fatalf("%v %s", err, out)
		}
	}
}
func TestLinuxInstallSetupAndDedicatedRemoval(t *testing.T) {
	f := newLinuxInstallerFixture(t)
	if out, err := f.run(t, []string{"CT_SKIP_SETUP=0"}, "--skip-setup"); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "setup-ran")); !os.IsNotExist(err) {
		t.Fatal("setup not skipped")
	}
	if out, err := f.run(t, []string{"CT_SKIP_SETUP=0", "CT_VERSION=1.2.3"}); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "setup-ran")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.dir, "setup-ran")); err != nil {
		t.Fatal(err)
	}
	if out, err := f.run(t, []string{"CT_UNINSTALL=1", "CT_YES=1"}); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if _, err := os.Stat(f.dir); !os.IsNotExist(err) {
		t.Fatal("dedicated directory remains")
	}
	if _, err := os.Stat(filepath.Join(f.root, "data/applications/clipboard-translator.desktop")); !os.IsNotExist(err) {
		t.Fatal("owned desktop remains")
	}
}

func TestLinuxInstallArchitectureAndSessionWarnings(t *testing.T) {
	f := newLinuxInstallerFixture(t)
	writeInstallFixture(t, filepath.Join(f.bin, "uname"), "#!/bin/sh\ncase $1 in -s) echo Linux ;; -m) echo aarch64 ;; esac\n", 0755)
	if out, err := f.run(t, nil); err == nil || !strings.Contains(out, "x86_64 is required") {
		t.Fatalf("%v %s", err, out)
	}
	if err := os.Remove(filepath.Join(f.bin, "uname")); err != nil {
		t.Fatal(err)
	}
	for _, session := range []string{"", "wayland"} {
		writeInstallFixture(t, filepath.Join(f.bin, "gdbus"), "#!/bin/sh\nexit 1\n", 0755)
		out, err := f.run(t, []string{"XDG_SESSION_TYPE=" + session})
		if err != nil {
			t.Fatalf("%v %s", err, out)
		}
		if !strings.Contains(out, "Warning:") || !strings.Contains(out, "Clipboard portals") {
			t.Fatalf("missing session warning: %s", out)
		}
	}
}

func TestLinuxInstallLaunchDetached(t *testing.T) {
	f := newLinuxInstallerFixture(t)
	// A stub records nohup's arguments without starting a desktop application.
	writeInstallFixture(t, filepath.Join(f.bin, "nohup"), "#!/bin/sh\nprintf '%s' \"$1\" > \"$HOME/launch-command\"\npwd > \"$HOME/launch-directory\"\n", 0755)
	out, err := f.run(t, []string{"CT_NO_LAUNCH=0"})
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	// Wait for the detached process, not a fixed sleep.
	deadline := time.Now().Add(3 * time.Second)
	for {
		b, err := os.ReadFile(filepath.Join(f.root, "launch-directory"))
		if err == nil && strings.TrimSpace(string(b)) == f.dir {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("launch did not use install directory: %s %v", b, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	b, err := os.ReadFile(filepath.Join(f.root, "launch-command"))
	if err != nil || string(b) != filepath.Join(f.dir, "clipboard-translator") {
		t.Fatalf("launch: %q %v", b, err)
	}
}

func TestLinuxInstallPipedDefaultDirectory(t *testing.T) {
	f := newLinuxInstallerFixture(t)
	script, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh")
	cmd.Stdin = strings.NewReader(string(script))
	cmd.Env = append(os.Environ(), "HOME="+f.root, "XDG_DATA_HOME=", "CT_INSTALL_DIR=", "CT_VERSION=1.2.3", "CT_SKIP_SETUP=1", "CT_NO_LAUNCH=1", "CT_UNINSTALL=0", "PATH="+f.bin+":"+os.Getenv("PATH"), "FIXTURE_ASSETS="+f.assets)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("piped install: %v %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(f.root, ".local/share/clipboard-translator/clipboard-translator")); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxInstallRejectsArchiveLinks(t *testing.T) {
	f := newLinuxInstallerFixture(t)
	archive := filepath.Join(f.assets, "clipboard-translator-linux-amd64-v1.2.3.tar.gz")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "clipboard-translator-linux-amd64-v1.2.3/clipboard-translator", Typeflag: tar.TypeSymlink, Linkname: "/tmp/victim"}); err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{tw.Close(), gz.Close(), file.Close()} {
		if err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	writeInstallFixture(t, archive+".sha256", fmt.Sprintf("%x\n", sha256.Sum256(b)), 0644)
	if out, err := f.run(t, nil); err == nil || !strings.Contains(out, "links or special files") {
		t.Fatalf("%v %s", err, out)
	}
	if _, err := os.Stat(f.dir); !os.IsNotExist(err) {
		t.Fatal("created install directory")
	}
}

func TestLinuxInstallDependencyInstructionsAndPortalKeyboard(t *testing.T) {
	f := newLinuxInstallerFixture(t)
	writeInstallFixture(t, filepath.Join(f.bin, "ldd"), "#!/bin/sh\necho 'libgtk-3.so.0 => not found'\n", 0755)
	writeInstallFixture(t, filepath.Join(f.bin, "gdbus"), `#!/bin/sh
case $1 in
 introspect) echo 'org.freedesktop.portal.GlobalShortcuts org.freedesktop.portal.RemoteDesktop org.freedesktop.portal.Clipboard' ;;
 call) printf '(<uint32 %s>,)\n' "$FIXTURE_DEVICES" ;;
esac
`, 0755)
	for _, devices := range []string{"1", "2"} {
		out, err := f.run(t, []string{"XDG_SESSION_TYPE=wayland", "FIXTURE_DEVICES=" + devices})
		if err != nil {
			t.Fatalf("%v %s", err, out)
		}
		if !strings.Contains(out, "sudo apt-get install libgtk-3-0t64 libxtst6 xdg-utils libnotify-bin") {
			t.Fatalf("missing dependency command: %s", out)
		}
		warned := strings.Contains(out, "Could not confirm them")
		if warned != (devices == "2") {
			t.Fatalf("devices=%s warning=%v: %s", devices, warned, out)
		}
	}
}
