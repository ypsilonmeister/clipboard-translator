package models

import (
	"os"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/testsupport"
)

func TestMain(m *testing.M) {
	// No test may contact the real model host.
	DownloadTransport = testsupport.LoopbackOnly()
	os.Exit(m.Run())
}
