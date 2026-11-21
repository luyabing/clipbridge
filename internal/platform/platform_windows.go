//go:build windows

package platform

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	runKeyName = "ClipBridge"
	runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
)

// AutostartEnabled reports whether the per-user Run entry points at this exe.
func AutostartEnabled() (bool, error) {
	exe, err := os.Executable()
	if err != nil {
		return false, err
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer k.Close()
	got, _, err := k.GetStringValue(runKeyName)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(got), `"`+exe+`"`), nil
}

// SetAutostart writes or removes the HKCU Run entry.
func SetAutostart(exePath, _ string, enabled bool) error {
	if !enabled {
		k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		defer k.Close()
		err = k.DeleteValue(runKeyName)
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	if exePath == "" {
		return fmt.Errorf("无法确定程序路径")
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(runKeyName, `"`+exePath+`"`)
}

// OpenURL opens the settings page in the default browser.
func OpenURL(url string) error {
	cmd := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url)
	return cmd.Start()
}

// OpenPath opens a folder in Explorer.
func OpenPath(path string) error {
	cmd := exec.Command("explorer.exe", path)
	return cmd.Start()
}

// DefaultDownloadDir returns the user's Downloads directory.
func DefaultDownloadDir() (string, error) {
	home, _ := os.UserHomeDir()
	fallback := filepath.Join(home, "Downloads")
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Explorer\Shell Folders`, registry.QUERY_VALUE)
	if err == nil {
		val, _, gerr := k.GetStringValue("{374DE290-123F-4565-9164-39C4925E467B}")
		k.Close()
		if gerr == nil && val != "" {
			return val, nil
		}
	}
	if home == "" {
		return "", fmt.Errorf("无法确定下载目录")
	}
	return fallback, nil
}

var (
	user32         = windows.NewLazySystemDLL("user32.dll")
	procMessageBox = user32.NewProc("MessageBoxW")
)

// Fatal shows a native error dialog. It never blocks app shutdown for long,
// but is only called during startup.
func Fatal(title, msg string) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(msg)
	const mbIconError = 0x00000010
	procMessageBox.Call(0,
		uintptr(unsafe.Pointer(m)),
		uintptr(unsafe.Pointer(t)),
		mbIconError)
}
