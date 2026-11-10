package main

import _ "embed"

// The setup scripts stay in the repository root (the one-line installer, the
// release zip and the tools depend on them), so they are embedded here and
// handed to the app.

//go:embed setup.ps1
var embeddedSetup []byte

//go:embed setup.sh
var embeddedLinuxSetup []byte

//go:embed setup-macos.sh
var embeddedMacSetup []byte
