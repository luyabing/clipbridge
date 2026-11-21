// Package platform contains the small amount of OS-specific glue needed for
// autostart, opening folders, and startup error dialogs.
package platform

import "runtime"

// OSName returns a short, user-facing OS name.
func OSName() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows"
	case "linux":
		return "Linux"
	case "darwin":
		return "macOS"
	default:
		return runtime.GOOS
	}
}
