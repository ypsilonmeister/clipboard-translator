// Package testsupport holds helpers shared by the test binaries of several
// packages. Import it from _test.go files only.
package testsupport

import (
	"fmt"
	"net/http"
)

// LoopbackOnly returns the transport every test download uses: a test that
// tries to reach a real host fails instead of downloading a model.
func LoopbackOnly() http.RoundTripper { return loopbackOnly{} }

type loopbackOnly struct{}

func (loopbackOnly) RoundTrip(r *http.Request) (*http.Response, error) {
	switch r.URL.Hostname() {
	case "127.0.0.1", "localhost", "::1":
		return http.DefaultTransport.RoundTrip(r)
	}
	return nil, fmt.Errorf("a test tried to reach %s", r.URL.Host)
}
