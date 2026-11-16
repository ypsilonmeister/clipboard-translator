package setup

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/download"
)

// progressInterval is the least time between two progress lines. They are
// plain lines, not a rewritten status line: on macOS the output goes to a
// log file, where "\r" updates would pile up on one line.
const progressInterval = time.Second

// download fetches rawURL into dest through the shared downloader
// (internal/download): it continues a "<dest>.partial" left by an earlier
// run, retries a few times when the connection drops or stalls, and gives the
// file its final name only after its SHA-256 matches want (an empty want
// skips the check, as in the scripts). A failed run leaves no file under the
// final name; the partial file is kept for the next run, except one that
// failed the hash check.
func (r *runner) download(rawURL, dest, want string) error {
	name := dest
	if i := strings.LastIndexAny(dest, `/\`); i >= 0 {
		name = dest[i+1:]
	}
	pr := &progressPrinter{r: r, name: name, last: r.clock()}
	o := download.Options{
		Client:       r.client,
		URL:          rawURL,
		Dest:         dest,
		SHA256:       want,
		UserAgent:    "ClipboardTranslatorSetup",
		StallTimeout: r.stall,
		Retries:      r.retries,
		RetryBase:    r.retryBase,
		Progress:     pr.update,
		OnResume: func(have int64) {
			r.printf("Continuing %s from %s of an earlier download", name, megabytes(have))
		},
		OnRetry: func(n int, err error, wait time.Duration) {
			r.printf("Download of %s was interrupted (%v); retrying in %s (%d of %d)", name, err, wait.Round(time.Second), n, r.retries)
		},
	}
	// Only GitHub's own API wants this media type; Hugging Face does not.
	if isGitHub(rawURL) {
		o.Accept = "application/vnd.github+json"
	}
	err := download.Fetch(context.Background(), o)
	if err != nil {
		var se *download.StatusError
		var hm *download.HashMismatchError
		if !errors.As(err, &se) && !errors.As(err, &hm) {
			return fmt.Errorf("download of %s failed: %w", rawURL, err)
		}
	}
	return err
}

func isGitHub(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	h := u.Hostname()
	return h == "github.com" || strings.HasSuffix(h, ".github.com")
}

// progressPrinter writes "  42% 756.0 / 1800.0 MB" at most once per
// progressInterval (percent left out when the size is unknown).
type progressPrinter struct {
	r    *runner
	name string
	last time.Time
}

func (p *progressPrinter) update(done, total int64) {
	now := p.r.clock()
	if now.Sub(p.last) < progressInterval {
		return
	}
	p.last = now
	if total > 0 && done <= total {
		p.r.printf("  %3d%%  %s / %s", done*100/total, megabytes(done), megabytes(total))
		return
	}
	p.r.printf("  %s", megabytes(done))
}

func megabytes(n int64) string { return fmt.Sprintf("%.1f MB", float64(n)/(1<<20)) }

// clock is time.Now unless a test replaced it.
func (r *runner) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}
