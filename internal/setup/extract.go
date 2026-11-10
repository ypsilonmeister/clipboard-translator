package setup

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The extraction below mirrors the safety contract Python's
// tarfile "data" filter gave the shell scripts: no absolute paths, no
// ".." escapes, no links that point outside the destination, no device
// files, and no setuid/setgid bits. Anything suspicious aborts the whole
// extraction, and because setup extracts into a staging folder that is
// then discarded, a hostile or corrupt archive installs nothing.

// withinDir reports whether path (already cleaned) is dest or inside it.
func withinDir(dest, path string) bool {
	rel, err := filepath.Rel(dest, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// memberPath validates one archive member name and returns where it goes.
func memberPath(dest, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("archive member with an empty name")
	}
	if filepath.IsAbs(name) || strings.HasPrefix(name, "/") || (len(name) >= 2 && name[1] == ':') {
		return "", fmt.Errorf("archive member %q has an absolute path", name)
	}
	target := filepath.Join(dest, filepath.FromSlash(name))
	if !withinDir(dest, target) {
		return "", fmt.Errorf("archive member %q would be written outside the destination", name)
	}
	return target, nil
}

// Archive entries must never be written through an earlier member's symlink.
// Even an internal alias can change how a later relative link is resolved.
func checkParentLinks(dest, dir string) error {
	if !withinDir(dest, dir) {
		return fmt.Errorf("archive member parent is outside the destination")
	}
	rel, err := filepath.Rel(dest, dir)
	if err != nil {
		return err
	}
	cur := dest
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		st, err := os.Lstat(cur)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive member parent %q is not a regular directory", cur)
		}
	}
	return nil
}

func checkRegularTarget(target string) error {
	st, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("archive member %q would overwrite a link or nonregular file", target)
	}
	return nil
}

func extractTarGz(archivePath, dest string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("%s is not a gzip archive: %w", filepath.Base(archivePath), err)
	}
	defer gz.Close()
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	var links []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			realDest, err := filepath.EvalSymlinks(dest)
			if err != nil {
				return err
			}
			for _, link := range links {
				real, err := filepath.EvalSymlinks(link)
				if err != nil || !withinDir(realDest, real) {
					return fmt.Errorf("archive link %q does not resolve inside the destination", link)
				}
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading %s: %w", filepath.Base(archivePath), err)
		}
		target, err := memberPath(dest, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := checkParentLinks(dest, target); err != nil {
				return err
			}
			if err := os.MkdirAll(target, fsMode(hdr)); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := checkParentLinks(dest, filepath.Dir(target)); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := checkParentLinks(dest, filepath.Dir(target)); err != nil {
				return err
			}
			if err := checkRegularTarget(target); err != nil {
				return err
			}
			w, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fsMode(hdr))
			if err != nil {
				return err
			}
			if _, err := io.Copy(w, tr); err != nil {
				w.Close()
				return err
			}
			if err := w.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink, tar.TypeLink:
			linkTarget, err := linkTargetPath(target, hdr.Linkname)
			if hdr.Typeflag == tar.TypeLink {
				linkTarget, err = memberPath(dest, hdr.Linkname)
			}
			if err != nil {
				return err
			}
			if !withinDir(dest, linkTarget) {
				return fmt.Errorf("archive member %q links outside the destination", hdr.Name)
			}
			if err := checkParentLinks(dest, filepath.Dir(target)); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target)
			if hdr.Typeflag == tar.TypeSymlink {
				if err := os.Symlink(hdr.Linkname, target); err != nil {
					return err
				}
				links = append(links, target)
			} else {
				if err := checkParentLinks(dest, filepath.Dir(linkTarget)); err != nil {
					return err
				}
				st, err := os.Lstat(linkTarget)
				if err != nil {
					return err
				}
				if !st.Mode().IsRegular() {
					return fmt.Errorf("archive hard link target is not a regular file")
				}
				if err := os.Link(linkTarget, target); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("archive member %q has unsupported type %d", hdr.Name, hdr.Typeflag)
		}
	}
}

// linkTargetPath resolves a link member's target against the link's own
// folder, rejecting absolute targets.
func linkTargetPath(linkPath, linkname string) (string, error) {
	if linkname == "" || filepath.IsAbs(linkname) || strings.HasPrefix(linkname, "/") || (len(linkname) >= 2 && linkname[1] == ':') {
		return "", fmt.Errorf("archive link target %q is not a relative path", linkname)
	}
	return filepath.Join(filepath.Dir(linkPath), filepath.FromSlash(linkname)), nil
}

func fsMode(hdr *tar.Header) os.FileMode {
	return os.FileMode(hdr.Mode) & 0o777
}

func extractZip(archivePath, dest string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("%s is not a zip archive: %w", filepath.Base(archivePath), err)
	}
	defer zr.Close()
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	for _, entry := range zr.File {
		target, err := memberPath(dest, entry.Name)
		if err != nil {
			return err
		}
		if entry.FileInfo().IsDir() {
			if err := checkParentLinks(dest, target); err != nil {
				return err
			}
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := checkParentLinks(dest, filepath.Dir(target)); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := checkParentLinks(dest, filepath.Dir(target)); err != nil {
			return err
		}
		mode := entry.Mode() & 0o777
		if mode == 0 {
			mode = 0o644
		}
		if err := checkRegularTarget(target); err != nil {
			return err
		}
		w, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		src, err := entry.Open()
		if err != nil {
			w.Close()
			return err
		}
		_, copyErr := io.Copy(w, src)
		src.Close()
		if err := w.Close(); copyErr == nil {
			copyErr = err
		}
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}

// findServerDirs returns the folders under root that contain a regular
// file called name (the llama-server executable), in walk order.
func findServerDirs(root, name string) []string {
	var dirs []string
	seen := map[string]bool{}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() && d.Name() == name {
			dir := filepath.Dir(path)
			if !seen[dir] {
				seen[dir] = true
				dirs = append(dirs, dir)
			}
		}
		return nil
	})
	return dirs
}
