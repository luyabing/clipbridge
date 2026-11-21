package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	cfg, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.DeviceID) != 32 {
		t.Fatalf("device ID length = %d", len(cfg.DeviceID))
	}
	if cfg.DeviceName == "" {
		t.Fatal("device name is empty")
	}
	if !cfg.AutoConnect || !cfg.SyncText || !cfg.SyncImage || !cfg.SyncFiles || !cfg.SkipSensitive {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.TCPPort != DefaultTCPPort || cfg.DiscoveryPort != DefaultDiscoveryPort || cfg.WebPort != DefaultWebPort {
		t.Fatalf("unexpected ports: %+v", cfg)
	}
	other, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if other.DeviceID == cfg.DeviceID {
		t.Fatal("two defaults share a device ID")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "config.json")
	cfg, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.DeviceName = "书房电脑"
	cfg.PairCode = "秘密口令"
	cfg.ManualPeers = []string{"192.168.122.5:45876", "10.0.0.8"}
	cfg.DownloadDir = filepath.Join(dir, "接收")
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := st.Mode().Perm(); perm != 0o600 {
			t.Fatalf("config permissions = %o, want 600", perm)
		}
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, loaded) {
		t.Fatalf("roundtrip mismatch:\n got %+v\nwant %+v", loaded, cfg)
	}
}

func TestLoadMissingCreatesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DeviceID == "" {
		t.Fatal("no device ID")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config was not created: %v", err)
	}
}

func TestValidateNormalizes(t *testing.T) {
	cfg, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.DeviceName = "  客厅电脑  "
	cfg.PairCode = "  123456  "
	cfg.ManualPeers = []string{" 192.168.1.2 ", "", "192.168.1.2", "192.168.1.2:45876", "192.168.1.2"}

	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.DeviceName != "客厅电脑" {
		t.Fatalf("device name = %q", cfg.DeviceName)
	}
	if cfg.PairCode != "123456" {
		t.Fatalf("pair code = %q", cfg.PairCode)
	}
	want := []string{"192.168.1.2", "192.168.1.2:45876"}
	if !reflect.DeepEqual(cfg.ManualPeers, want) {
		t.Fatalf("manual peers = %#v, want %#v", cfg.ManualPeers, want)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Config)
		want string
	}{
		{"empty name", func(c *Config) { c.DeviceName = "   " }, "设备名称"},
		{"long name", func(c *Config) { c.DeviceName = strings.Repeat("电", 61) }, "60"},
		{"long code", func(c *Config) { c.PairCode = strings.Repeat("x", 129) }, "配对码"},
		{"short id", func(c *Config) { c.DeviceID = "abc" }, "设备 ID"},
		{"low port", func(c *Config) { c.TCPPort = 80 }, "1024"},
		{"high port", func(c *Config) { c.WebPort = 70000 }, "65535"},
		{"duplicate port", func(c *Config) { c.DiscoveryPort = c.TCPPort }, "互不相同"},
		{"zero file limit", func(c *Config) { c.MaxFileMB = 0 }, "1–2048"},
		{"huge file limit", func(c *Config) { c.MaxFileMB = 2049 }, "1–2048"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Default()
			if err != nil {
				t.Fatal(err)
			}
			tc.mut(cfg)
			err = cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func TestCloneIsDeepCopy(t *testing.T) {
	cfg, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.ManualPeers = []string{"10.0.0.1"}
	cp := cfg.Clone()
	cp.ManualPeers[0] = "10.0.0.2"
	if cfg.ManualPeers[0] != "10.0.0.1" {
		t.Fatal("clone shares the manual peers slice")
	}
}

func TestLoadPartialConfigKeepsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	raw := `{"deviceName":"手写配置","tcpPort":46000,"webPort":46002,"discoveryPort":46001}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DeviceName != "手写配置" {
		t.Fatalf("device name = %q", cfg.DeviceName)
	}
	if !cfg.AutoConnect || !cfg.SyncText || !cfg.SyncImage || !cfg.SyncFiles || !cfg.SkipSensitive {
		t.Fatalf("missing boolean fields did not keep their defaults: %+v", cfg)
	}
	if cfg.MaxFileMB != DefaultMaxFileMB {
		t.Fatalf("max file size = %d", cfg.MaxFileMB)
	}
	if cfg.PairCode != "" || cfg.Paused {
		t.Fatalf("unexpected pair/paused values: %+v", cfg)
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
