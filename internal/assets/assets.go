// Package assets holds images embedded in the binary so the app stays a
// single portable file.
package assets

import _ "embed"

// IconPNG is the 256x256 application icon used on Linux and in the web UI.
//
//go:embed icon.png
var IconPNG []byte

// IconICO is the multi-resolution icon used by the Windows tray and exe.
//
//go:embed icon.ico
var IconICO []byte
