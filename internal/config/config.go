// Package config loads and persists ClipBridge settings.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	DefaultTCPPort       = 45876
	DefaultDiscoveryPort = 45875
	DefaultWebPort       = 45878
	DefaultMaxFileMB     = 512
)

// Config is stored as JSON in the user's config directory.
type Config struct {
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`

	// PairCode is optional. When set on both ends, connections are
	// authenticated and encrypted with a key derived from it.
	PairCode string `json:"pairCode"`

	AutoConnect   bool `json:"autoConnect"`
	SyncText      bool `json:"syncText"`
	SyncImage     bool `json:"syncImage"`
	SyncFiles     bool `json:"syncFiles"`
	SkipSensitive bool `json:"skipSensitive"`
	Paused        bool `json:"paused"`

	ManualPeers []string `json:"manualPeers"`

	TCPPort       int `json:"tcpPort"`
	DiscoveryPort int `json:"discoveryPort"`
	WebPort       int `json:"webPort"`
	MaxFileMB     int `json:"maxFileMB"`

	// DownloadDir overrides the default location for received files.
	DownloadDir string `json:"downloadDir,omitempty"`
}

// DefaultPath returns the per-user config file path.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "clipbridge", "config.json"), nil
}

// Load reads a config file, creating it with defaults when absent.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg, derr := Default()
		if derr != nil {
			return nil, derr
		}
		if err := Save(path, cfg); err != nil {
			return nil, err
		}
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	// Start from defaults so fields added in a later version (and fields
	// missing from a hand-written or older file) keep their intended value
	// instead of becoming zero values. Explicit JSON values still win.
	defaults, err := Default()
	if err != nil {
		return nil, err
	}
	cfg := *defaults
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("配置文件格式错误（%s）：%w", path, err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("配置文件无效（%s）：%w", path, err)
	}
	return &cfg, nil
}

// Save writes the config atomically with owner-only permissions.
func Save(path string, cfg *Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// Default builds a configuration with a fresh device ID and hostname.
func Default() (*Config, error) {
	id, err := newDeviceID()
	if err != nil {
		return nil, err
	}
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "我的电脑"
	}
	cfg := &Config{
		DeviceID:      id,
		DeviceName:    host,
		AutoConnect:   true,
		SyncText:      true,
		SyncImage:     true,
		SyncFiles:     true,
		SkipSensitive: true,
		TCPPort:       DefaultTCPPort,
		DiscoveryPort: DefaultDiscoveryPort,
		WebPort:       DefaultWebPort,
		MaxFileMB:     DefaultMaxFileMB,
	}
	cfg.applyDefaults()
	return cfg, cfg.Validate()
}

func (c *Config) applyDefaults() {
	if c.DeviceID == "" {
		c.DeviceID, _ = newDeviceID()
	}
	if c.DeviceName == "" {
		if h, err := os.Hostname(); err == nil {
			c.DeviceName = h
		} else {
			c.DeviceName = "我的电脑"
		}
	}
	if c.TCPPort == 0 {
		c.TCPPort = DefaultTCPPort
	}
	if c.DiscoveryPort == 0 {
		c.DiscoveryPort = DefaultDiscoveryPort
	}
	if c.WebPort == 0 {
		c.WebPort = DefaultWebPort
	}
	if c.MaxFileMB == 0 {
		c.MaxFileMB = DefaultMaxFileMB
	}
	if c.ManualPeers == nil {
		c.ManualPeers = []string{}
	}
}

// Validate normalizes user input and rejects invalid values.
func (c *Config) Validate() error {
	c.DeviceName = strings.TrimSpace(c.DeviceName)
	if c.DeviceName == "" {
		return errors.New("设备名称不能为空")
	}
	if utf8.RuneCountInString(c.DeviceName) > 60 {
		return errors.New("设备名称不能超过 60 个字符")
	}
	if !utf8.ValidString(c.DeviceName) {
		return errors.New("设备名称不是有效文本")
	}
	c.PairCode = strings.TrimSpace(c.PairCode)
	if len(c.PairCode) > 128 {
		return errors.New("配对码不能超过 128 个字符")
	}
	if c.DeviceID == "" || len(c.DeviceID) < 16 || len(c.DeviceID) > 128 {
		return errors.New("设备 ID 无效")
	}
	for _, p := range []struct {
		name string
		port int
	}{
		{"TCP 端口", c.TCPPort},
		{"发现端口", c.DiscoveryPort},
		{"网页端口", c.WebPort},
	} {
		if p.port < 1024 || p.port > 65535 {
			return fmt.Errorf("%s必须在 1024–65535 之间", p.name)
		}
	}
	if c.TCPPort == c.DiscoveryPort || c.TCPPort == c.WebPort || c.DiscoveryPort == c.WebPort {
		return errors.New("TCP、发现和网页端口必须互不相同")
	}
	if c.MaxFileMB < 1 || c.MaxFileMB > 2048 {
		return errors.New("单次文件传输上限必须在 1–2048 MB 之间")
	}

	seen := make(map[string]bool, len(c.ManualPeers))
	out := make([]string, 0, len(c.ManualPeers))
	for _, raw := range c.ManualPeers {
		addr := strings.TrimSpace(raw)
		if addr == "" {
			continue
		}
		key := strings.ToLower(addr)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, addr)
		if len(out) >= 64 {
			break
		}
	}
	c.ManualPeers = out
	return nil
}

// Clone returns a deep copy safe to hand to another goroutine.
func (c *Config) Clone() Config {
	cp := *c
	cp.ManualPeers = append([]string(nil), c.ManualPeers...)
	return cp
}

func newDeviceID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
