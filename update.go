package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// version is the release this binary was built from. release.yml sets it with
// -ldflags "-X main.version=<tag without v>"; a local build stays "dev".
var version = "dev"

const (
	repoSlug         = "ypsilonmeister/clipboard-translator"
	latestReleaseAPI = "https://api.github.com/repos/" + repoSlug + "/releases/latest"
	releasesPage     = "https://github.com/" + repoSlug + "/releases"
	installScriptURL = "https://raw.githubusercontent.com/" + repoSlug + "/main/install.ps1"
	plamoLicenseURL  = "https://plamo.preferredai.jp/info/plamo-community-license-ja"
)

type releaseInfo struct{ Tag, URL string }

// fetchLatest asks the GitHub API for the newest published release. It is only
// called when the user picks "Check for updates"; the app never phones home on
// its own.
func fetchLatest(ctx context.Context, apiURL string) (releaseInfo, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return releaseInfo{}, err
	}
	req.Header.Set("User-Agent", "ClipboardTranslator/"+version)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return releaseInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return releaseInfo{}, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var body struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return releaseInfo{}, err
	}
	if body.TagName == "" {
		return releaseInfo{}, fmt.Errorf("the release has no tag")
	}
	return releaseInfo{Tag: body.TagName, URL: body.HTMLURL}, nil
}

// versionParts reads "v1.2.3", "1.2.3-rc1" or "1.2" as numbers. It returns nil
// for anything that does not start with a digit ("dev").
func versionParts(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" || v[0] < '0' || v[0] > '9' {
		return nil
	}
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	var out []int
	for _, p := range strings.Split(v, ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	return out
}

// compareVersions compares numerically, so 0.10.0 is newer than 0.9.0. Missing
// parts count as 0. It returns 0 when either side is not a version.
func compareVersions(a, b string) int {
	pa, pb := versionParts(a), versionParts(b)
	if pa == nil || pb == nil {
		return 0
	}
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	return 0
}

func isNewerVersion(latest, current string) bool { return compareVersions(latest, current) > 0 }

// installKind says how the running copy got here, which decides how it updates.
type installKind int

const (
	kindPortable  installKind = iota // a zip extracted by hand
	kindInstaller                    // installed by install.ps1
	kindScoop                        // installed by Scoop
)

// detectInstallKind looks at the app folder and at the InstallLocation that
// install.ps1 records for "Apps & features".
func detectInstallKind(dir, installLocation string) installKind {
	// Windows paths: case-insensitive, either slash, optional trailing slash.
	norm := func(p string) string { return strings.TrimRight(strings.ToLower(strings.ReplaceAll(p, `\`, "/")), "/") }
	if strings.Contains(norm(dir)+"/", "/scoop/apps/") {
		return kindScoop
	}
	if installLocation != "" && norm(installLocation) == norm(dir) {
		return kindInstaller
	}
	return kindPortable
}
