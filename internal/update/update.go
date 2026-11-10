package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

const (
	RepoSlug         = "ypsilonmeister/clipboard-translator"
	LatestReleaseAPI = "https://api.github.com/repos/" + RepoSlug + "/releases/latest"
	ReleasesPage     = "https://github.com/" + RepoSlug + "/releases"
	PlamoLicenseURL  = "https://plamo.preferredai.jp/info/plamo-community-license-ja"
)

type ReleaseInfo struct{ Tag, URL string }

var releaseTagPattern = regexp.MustCompile(`^v[0-9]+(\.[0-9]+)*$`)

// installScriptURL pins the updater itself to the release the user confirmed.
func InstallScriptURL(tag string) (string, error) {
	if !releaseTagPattern.MatchString(tag) {
		return "", fmt.Errorf("invalid release tag %q: expected v followed by dot-separated numbers (for example v0.7.0)", tag)
	}
	return "https://raw.githubusercontent.com/" + RepoSlug + "/" + tag + "/install.ps1", nil
}

// fetchLatest asks the GitHub API for the newest published release. It is only
// called when the user picks "Check for updates"; the app never phones home on
// its own.
func FetchLatest(ctx context.Context, apiURL, version string) (ReleaseInfo, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return ReleaseInfo{}, err
	}
	req.Header.Set("User-Agent", "ClipboardTranslator/"+version)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ReleaseInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ReleaseInfo{}, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var body struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return ReleaseInfo{}, err
	}
	if body.TagName == "" {
		return ReleaseInfo{}, fmt.Errorf("the release has no tag")
	}
	return ReleaseInfo{Tag: body.TagName, URL: body.HTMLURL}, nil
}

// versionParts reads "v1.2.3", "1.2.3-rc1" or "1.2" as numbers. It returns nil
// for anything that does not start with a digit ("dev").
func VersionParts(v string) []int {
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
func CompareVersions(a, b string) int {
	pa, pb := VersionParts(a), VersionParts(b)
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

func IsNewerVersion(latest, current string) bool { return CompareVersions(latest, current) > 0 }

// installKind says how the running copy got here, which decides how it updates.
type InstallKind int

const (
	KindPortable  InstallKind = iota // a zip extracted by hand
	KindInstaller                    // installed by install.ps1
	KindScoop                        // installed by Scoop
)

// detectInstallKind looks at the app folder and at the InstallLocation that
// install.ps1 records for "Apps & features".
func DetectInstallKind(dir, installLocation string) InstallKind {
	// Windows paths: case-insensitive, either slash, optional trailing slash.
	norm := func(p string) string { return strings.TrimRight(strings.ToLower(strings.ReplaceAll(p, `\`, "/")), "/") }
	if strings.Contains(norm(dir)+"/", "/scoop/apps/") {
		return KindScoop
	}
	if installLocation != "" && norm(installLocation) == norm(dir) {
		return KindInstaller
	}
	return KindPortable
}
