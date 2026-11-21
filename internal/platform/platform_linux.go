//go:build linux

package platform

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const autostartFile = "clipbridge.desktop"

// AutostartEnabled reports whether the XDG autostart entry is present and on.
func AutostartEnabled() (bool, error) {
	path, err := autostartPath()
	if err != nil {
		return false, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	text := string(raw)
	if strings.Contains(text, "Hidden=true") || strings.Contains(text, "X-GNOME-Autostart-enabled=false") {
		return false, nil
	}
	return true, nil
}

// SetAutostart creates or removes ~/.config/autostart/clipbridge.desktop.
func SetAutostart(exePath, iconPath string, enabled bool) error {
	path, err := autostartPath()
	if err != nil {
		return err
	}
	if !enabled {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if exePath == "" {
		return fmt.Errorf("无法确定程序路径")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	iconLine := ""
	if iconPath != "" {
		iconLine = "Icon=" + iconPath + "\n"
	}
	content := "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Version=1.0\n" +
		"Name=剪贴桥\n" +
		"Name[en]=ClipBridge\n" +
		"Comment=局域网剪贴板共享\n" +
		"Exec=" + quoteDesktopExec(exePath) + "\n" +
		iconLine +
		"Terminal=false\n" +
		"StartupNotify=false\n" +
		"X-GNOME-Autostart-enabled=true\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	return nil
}

func autostartPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "autostart", autostartFile), nil
}

// quoteDesktopExec quotes one executable path for a Desktop Entry Exec key.
func quoteDesktopExec(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\', '`', '$':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '%':
			b.WriteString("%%")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// OpenURL opens the settings page in the user's default browser.
func OpenURL(url string) error {
	cmd := exec.Command("xdg-open", url)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}

// OpenPath opens a folder in the user's file manager.
func OpenPath(path string) error {
	cmd := exec.Command("xdg-open", path)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}

// DefaultDownloadDir returns the user's Downloads directory.
func DefaultDownloadDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(home, ".config")
	}
	if f, err := os.Open(filepath.Join(cfg, "user-dirs.dirs")); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if !strings.HasPrefix(line, "XDG_DOWNLOAD_DIR=") {
				continue
			}
			val := strings.TrimPrefix(line, "XDG_DOWNLOAD_DIR=")
			if unq, err := strconv.Unquote(val); err == nil {
				val = unq
			}
			val = os.ExpandEnv(val)
			if filepath.IsAbs(val) {
				return val, nil
			}
		}
	}
	return filepath.Join(home, "Downloads"), nil
}

// Fatal shows a best-effort desktop error dialog, then writes to stderr.
func Fatal(title, msg string) {
	if path, err := exec.LookPath("zenity"); err == nil {
		cmd := exec.Command(path, "--error", "--title", title, "--text", msg, "--width", "420")
		if cmd.Run() == nil {
			return
		}
	}
	if path, err := exec.LookPath("notify-send"); err == nil {
		_ = exec.Command(path, "--urgency=critical", title, msg).Run()
	}
	fmt.Fprintf(os.Stderr, "%s: %s\n", title, msg)
}
