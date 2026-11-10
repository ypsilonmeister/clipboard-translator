// Package assets embeds the files under assets/ that Go code needs. The
// directory also holds the sources of the Windows resources and the macOS icon
// (read by tools/mksyso, tools/mkicon and tools/package-macos.sh), which are
// not embedded here.
package assets

import _ "embed"

// IconPNG is the application icon, used as the Linux tray icon.
//
//go:embed icon.png
var IconPNG []byte
