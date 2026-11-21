// Package app wires clipboard capture, peer discovery, networking, the local
// web control panel, and the tray icon together.
package app

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
	"unicode/utf8"

	"clipbridge/internal/assets"
	"clipbridge/internal/config"
	"clipbridge/internal/platform"
	"clipbridge/internal/proto"
)

// ErrAlreadyRunning means the local web control panel is already served by
// another ClipBridge process.
var ErrAlreadyRunning = errors.New("剪贴桥已经在运行")

// Options configures a new App.
type Options struct {
	ConfigPath string
	Version    string
	Headless   bool
	Debug      bool
	Clipboard  Clipboard // nil means the real system clipboard
}

// App is the long-lived ClipBridge process.
type App struct {
	version  string
	headless bool
	debug    bool
	cfgPath  string
	cfgDir   string
	exePath  string
	iconPath string
	logPath  string

	cfgMu sync.RWMutex
	cfg   config.Config

	logger   *log.Logger
	logFile  *os.File
	events   *eventLog
	stats    counters
	transfer transferTracker

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	wg     sync.WaitGroup

	clip   Clipboard
	hub    *hub
	disc   *discovery
	web    *webServer
	tray   *trayState
	trayMu sync.Mutex

	downloadRoot string
	localIPs     []string
	startedAt    time.Time
	shutdownOnce sync.Once

	ignoreMu    sync.Mutex
	ignoreUntil time.Time
	lastHash    [32]byte
	hasHash     bool

	fileEchoMu sync.Mutex
	fileEcho   map[[32]byte]time.Time

	autoMu       sync.Mutex
	autostart    bool
	autoStartErr error
}

// New loads configuration and initializes the clipboard backend.
func New(opts Options) (*App, error) {
	if opts.ConfigPath == "" {
		p, err := config.DefaultPath()
		if err != nil {
			return nil, err
		}
		opts.ConfigPath = p
	}
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return nil, err
	}
	cfgDir := filepath.Dir(opts.ConfigPath)
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		return nil, err
	}

	logPath := filepath.Join(cfgDir, "clipbridge.log")
	logFile, err := openLogFile(logPath)
	if err != nil {
		return nil, err
	}
	logger := log.New(logFile, "", log.LstdFlags|log.Lmicroseconds)

	clip := opts.Clipboard
	if clip == nil {
		clip, err = NewSystemClipboard(opts.Debug, logger)
		if err != nil {
			logFile.Close()
			return nil, err
		}
	}
	exe, _ := os.Executable()
	ctx, cancel := context.WithCancel(context.Background())
	app := &App{
		version:   opts.Version,
		headless:  opts.Headless,
		debug:     opts.Debug,
		cfgPath:   opts.ConfigPath,
		cfgDir:    cfgDir,
		exePath:   exe,
		iconPath:  filepath.Join(cfgDir, "icon.png"),
		logPath:   logPath,
		cfg:       *cfg,
		logger:    logger,
		logFile:   logFile,
		events:    newEventLog(60),
		ctx:       ctx,
		cancel:    cancel,
		done:      make(chan struct{}),
		clip:      clip,
		startedAt: time.Now(),
	}
	app.localIPs = localIPv4s()
	app.downloadRoot = app.computeDownloadRoot()
	app.autoStartErr = app.loadAutostart()
	return app, nil
}

func openLogFile(path string) (*os.File, error) {
	const rotateAt = 5 << 20
	if st, err := os.Stat(path); err == nil && st.Size() > rotateAt {
		_ = os.Rename(path, path+".old")
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

// Start begins serving the control panel, listening for peers, and watching
// the clipboard. It returns ErrAlreadyRunning when another instance owns the
// web port.
func (a *App) Start() error {
	if err := a.writeIcon(); err != nil {
		a.logf("写入图标失败: %v", err)
	}
	if err := os.MkdirAll(a.downloadRoot, 0o755); err != nil {
		a.logf("创建接收目录失败: %v", err)
	}

	if err := a.startWeb(); err != nil {
		return err
	}

	a.hub = newHub(a)
	if err := a.hub.Start(); err != nil {
		a.Shutdown()
		return err
	}
	a.disc = newDiscovery(a)
	a.disc.Start()
	a.startClipboard()
	a.startMaintenance()
	if !a.headless {
		a.startTray()
	}
	a.events.Add("sys", "config", "剪贴桥已启动")
	a.logf("剪贴桥 %s 已启动，控制面板 %s", a.version, a.URL())
	return nil
}

func (a *App) writeIcon() error {
	cur, err := os.ReadFile(a.iconPath)
	if err == nil && len(cur) == len(assets.IconPNG) {
		return nil
	}
	return os.WriteFile(a.iconPath, assets.IconPNG, 0o644)
}

// Done is closed after Shutdown.
func (a *App) Done() <-chan struct{} { return a.done }

// URL is the local control panel address.
func (a *App) URL() string {
	cfg := a.Config()
	return fmt.Sprintf("http://127.0.0.1:%d/", cfg.WebPort)
}

// Version returns the build version.
func (a *App) Version() string { return a.version }

// Shutdown stops every subsystem. It is safe to call more than once.
func (a *App) Shutdown() {
	a.shutdownOnce.Do(func() {
		a.cancel()
		if t := a.trayHandle(); t != nil {
			t.stop()
		}
		if a.disc != nil {
			a.disc.Shutdown()
		}
		if a.hub != nil {
			a.hub.Shutdown()
		}
		if a.web != nil {
			a.web.Shutdown()
		}
		a.wg.Wait()
		close(a.done)
		a.logf("剪贴桥已退出")
		if a.logFile != nil {
			a.logFile.Close()
		}
	})
}

// Config returns a copy of the current configuration.
func (a *App) Config() config.Config {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	return a.cfg.Clone()
}

// UpdateConfig validates, persists, and applies a configuration change.
func (a *App) UpdateConfig(mut func(*config.Config) error) error {
	a.cfgMu.Lock()
	next := a.cfg.Clone()
	if err := mut(&next); err != nil {
		a.cfgMu.Unlock()
		return err
	}
	if err := next.Validate(); err != nil {
		a.cfgMu.Unlock()
		return err
	}
	if err := config.Save(a.cfgPath, &next); err != nil {
		a.cfgMu.Unlock()
		return err
	}
	prev := a.cfg
	a.cfg = next
	a.cfgMu.Unlock()

	a.afterConfigChange(prev, next)
	return nil
}

func (a *App) afterConfigChange(prev, next config.Config) {
	if prev.Paused != next.Paused {
		a.syncTrayState()
		if next.Paused {
			a.events.Add("sys", "config", "已暂停同步")
		} else {
			a.events.Add("sys", "config", "已恢复同步")
		}
	}
	if prev.PairCode != next.PairCode {
		if a.hub != nil {
			a.hub.CloseAll("配对码已更改")
		}
		a.events.Add("sys", "config", "配对码已更新，正在重新连接")
	}
	if prev.DeviceName != next.DeviceName && a.disc != nil {
		a.disc.Trigger()
	}
	if !prev.AutoConnect && next.AutoConnect && a.disc != nil {
		a.disc.Trigger()
	}
	if len(prev.ManualPeers) != len(next.ManualPeers) {
		a.dialManualPeers()
	}
	a.syncTrayState()
}

// SetPaused toggles clipboard synchronization without stopping the network.
func (a *App) SetPaused(paused bool) error {
	return a.UpdateConfig(func(c *config.Config) error {
		c.Paused = paused
		return nil
	})
}

// SetAutostart enables or disables login autostart.
func (a *App) SetAutostart(enabled bool) error {
	if err := platform.SetAutostart(a.exePath, a.iconPath, enabled); err != nil {
		a.autoMu.Lock()
		a.autoStartErr = err
		a.autoMu.Unlock()
		return err
	}
	a.autoMu.Lock()
	a.autostart = enabled
	a.autoStartErr = nil
	a.autoMu.Unlock()
	a.events.Add("sys", "config", map[bool]string{true: "已开启开机自启", false: "已关闭开机自启"}[enabled])
	a.syncTrayState()
	return nil
}

func (a *App) loadAutostart() error {
	on, err := platform.AutostartEnabled()
	a.autoMu.Lock()
	a.autostart = on
	a.autoMu.Unlock()
	return err
}

// AutostartStatus returns the cached autostart state.
func (a *App) AutostartStatus() (bool, error) {
	a.autoMu.Lock()
	defer a.autoMu.Unlock()
	return a.autostart, a.autoStartErr
}

func (a *App) computeDownloadRoot() string {
	cfg := a.Config()
	if cfg.DownloadDir != "" {
		return cfg.DownloadDir
	}
	base, err := platform.DefaultDownloadDir()
	if err != nil || base == "" {
		base, _ = os.UserHomeDir()
		if base == "" {
			base = os.TempDir()
		}
	}
	return filepath.Join(base, "ClipBridge")
}

func localIPv4s() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			s := ip.String()
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	sort.Strings(out)
	return out
}

// startClipboard connects the OS clipboard stream to the network layer.
func (a *App) startClipboard() {
	ch := a.clip.Watch(a.ctx)
	a.wg.Add(1)
	go a.clipboardLoop(ch)
}

func (a *App) clipboardLoop(ch <-chan ClipEvent) {
	defer a.wg.Done()
	for {
		var first ClipEvent
		select {
		case <-a.ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			first = ev
		}
		batch := map[ClipKind]ClipEvent{first.Kind: first}
		timer := time.NewTimer(180 * time.Millisecond)
	closed:
		for {
			select {
			case <-a.ctx.Done():
				timer.Stop()
				return
			case ev, ok := <-ch:
				if !ok {
					timer.Stop()
					break closed
				}
				batch[ev.Kind] = ev
			case <-timer.C:
				break closed
			}
		}
		a.handleLocalBatch(batch)
	}
}

// handleLocalBatch picks one representation (files > image > text) from a
// burst of clipboard events and sends it to all connected peers.
func (a *App) handleLocalBatch(batch map[ClipKind]ClipEvent) {
	cfg := a.Config()
	if cfg.Paused {
		return
	}
	var ev ClipEvent
	var kind ClipKind
	found := false
	for _, k := range []ClipKind{ClipFiles, ClipImage, ClipText} {
		if e, ok := batch[k]; ok {
			ev, kind, found = e, k, true
			break
		}
	}
	if !found {
		return
	}
	if kind == ClipFiles && len(ev.Files) == 0 {
		ev.Files = ParseURIList(string(ev.Data))
	}
	if kind == ClipFiles && a.filesRecentlyTransferred(ev.Files) {
		a.debugf("跳过刚传输过的文件回声")
		return
	}

	h := contentHash(kind, ev.Data, ev.Files)
	if a.suppressed(kind, h) {
		a.debugf("跳过自己的剪贴板回声：类型=%s", kind)
		return
	}
	a.rememberLocal(h)
	a.debugf("处理本地剪贴板：类型=%s 大小=%d 文件=%d 敏感=%v", kind, len(ev.Data), len(ev.Files), ev.Sensitive)

	if ev.Sensitive && cfg.SkipSensitive {
		a.events.Add("sys", "config", "已跳过密码管理器标记的内容")
		return
	}

	enabled := false
	switch kind {
	case ClipText:
		enabled = cfg.SyncText
	case ClipImage:
		enabled = cfg.SyncImage
	case ClipFiles:
		enabled = cfg.SyncFiles
	}
	if !enabled {
		return
	}
	if a.hub == nil {
		return
	}

	switch kind {
	case ClipText:
		if err := ValidateText(ev.Data); err != nil {
			a.events.Add("sys", "error", "剪贴板文本未发送："+err.Error())
			return
		}
		n := a.hub.Broadcast(proto.TypeText, ev.Data)
		a.debugf("发送文本到 %d 台设备", n)
		if n > 0 {
			a.stats.sent.Add(1)
			a.stats.bytesOut.Add(int64(len(ev.Data) * n))
			a.events.Add("out", "text", fmt.Sprintf("已发送文本（%d 字）到 %d 台设备", a.charCount(ev.Data), n))
		}
	case ClipImage:
		if err := ValidatePNG(ev.Data); err != nil {
			a.events.Add("sys", "error", "剪贴板图片未发送："+err.Error())
			return
		}
		n := a.hub.Broadcast(proto.TypeImage, ev.Data)
		if n > 0 {
			a.stats.sent.Add(1)
			a.stats.bytesOut.Add(int64(len(ev.Data) * n))
			a.events.Add("out", "image", fmt.Sprintf("已发送图片（%s）到 %d 台设备", humanBytes(int64(len(ev.Data))), n))
		}
	case ClipFiles:
		files, total, err := a.collectOutgoingFiles(ev.Files, cfg.MaxFileMB)
		if err != nil {
			a.events.Add("sys", "error", "文件未发送："+err.Error())
			return
		}
		n := a.hub.BroadcastFiles(files, total)
		if n > 0 {
			a.rememberTransferredFiles(ev.Files)
			a.stats.sent.Add(1)
			a.stats.bytesOut.Add(total * int64(n))
			a.events.Add("out", "files", fmt.Sprintf("正在发送 %d 个文件（%s）到 %d 台设备", len(files), humanBytes(total), n))
		}
	}
}

func (a *App) debugf(format string, args ...any) {
	if a.debug {
		a.logger.Printf("DEBUG "+format, args...)
	}
}

func (a *App) logf(format string, args ...any) {
	a.logger.Printf(format, args...)
}

func (a *App) closeWithLog(c io.Closer) {
	if c != nil {
		if err := c.Close(); err != nil {
			a.debugf("关闭失败: %v", err)
		}
	}
}

// helper used by web status probe
func (a *App) appMarker() string { return "clipbridge" }

func (a *App) probeExistingInstance() bool {
	client := &http.Client{Timeout: 700 * time.Millisecond}
	url := a.URL() + "api/status"
	for i := 0; i < 5; i++ {
		resp, err := client.Get(url)
		if err == nil {
			var probe struct {
				App string `json:"app"`
			}
			dec := json.NewDecoder(io.LimitReader(resp.Body, 4096))
			_ = dec.Decode(&probe)
			resp.Body.Close()
			if probe.App == a.appMarker() {
				return true
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	return false
}

func (a *App) openSettings() {
	if err := platform.OpenURL(a.URL()); err != nil {
		a.events.Add("sys", "error", "无法打开浏览器："+err.Error())
	}
}

func (a *App) openPath(path string) {
	if err := platform.OpenPath(path); err != nil {
		a.events.Add("sys", "error", "无法打开文件夹："+err.Error())
	}
}

// markRemote remembers remote content so the watcher does not echo it.
func (a *App) markRemote(kind ClipKind, data []byte, files []string) {
	h := contentHash(kind, data, files)
	a.ignoreMu.Lock()
	a.lastHash = h
	a.hasHash = true
	a.ignoreUntil = time.Now().Add(2500 * time.Millisecond)
	a.ignoreMu.Unlock()
}

func (a *App) suppressed(kind ClipKind, h [32]byte) bool {
	a.ignoreMu.Lock()
	defer a.ignoreMu.Unlock()
	if a.hasHash && h == a.lastHash {
		return true
	}
	// Some backends re-encode images when handing them back through the
	// watcher, so the bytes do not always match what we just wrote. Ignore a
	// short window of image events to avoid an endless image echo loop. Text
	// and file lists are matched exactly and do not need that window.
	return kind == ClipImage && time.Now().Before(a.ignoreUntil)
}

func (a *App) rememberLocal(h [32]byte) {
	a.ignoreMu.Lock()
	a.lastHash = h
	a.hasHash = true
	a.ignoreMu.Unlock()
}

// fileSetHash fingerprints a file list by name and size. Paths are deliberately
// left out: when another machine receives these files it saves them under a
// different path, but the names and sizes still identify the same set.
func fileSetHash(paths []string) ([32]byte, bool) {
	var zero [32]byte
	if len(paths) == 0 {
		return zero, false
	}
	type meta struct {
		name string
		size int64
	}
	metas := make([]meta, 0, len(paths))
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil || !st.Mode().IsRegular() {
			return zero, false
		}
		metas = append(metas, meta{name: filepath.Base(p), size: st.Size()})
	}
	sort.Slice(metas, func(i, j int) bool {
		if metas[i].name == metas[j].name {
			return metas[i].size < metas[j].size
		}
		return metas[i].name < metas[j].name
	})
	h := sha256.New()
	for _, m := range metas {
		fmt.Fprintf(h, "%s\x00%d\n", m.name, m.size)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, true
}

const fileEchoWindow = 8 * time.Second

// rememberTransferredFiles remembers a file set that just crossed the network
// so the watcher does not treat its own received copy as a fresh local copy.
func (a *App) rememberTransferredFiles(paths []string) {
	h, ok := fileSetHash(paths)
	if !ok {
		return
	}
	now := time.Now()
	a.fileEchoMu.Lock()
	if a.fileEcho == nil {
		a.fileEcho = make(map[[32]byte]time.Time)
	}
	for k, exp := range a.fileEcho {
		if now.After(exp) {
			delete(a.fileEcho, k)
		}
	}
	a.fileEcho[h] = now.Add(fileEchoWindow)
	a.fileEchoMu.Unlock()
}

func (a *App) filesRecentlyTransferred(paths []string) bool {
	h, ok := fileSetHash(paths)
	if !ok {
		return false
	}
	now := time.Now()
	a.fileEchoMu.Lock()
	defer a.fileEchoMu.Unlock()
	exp, ok := a.fileEcho[h]
	if !ok {
		return false
	}
	if now.After(exp) {
		delete(a.fileEcho, h)
		return false
	}
	return true
}

func contentHash(kind ClipKind, data []byte, files []string) [32]byte {
	h := sha256.New()
	var b [5]byte
	binary.BigEndian.PutUint32(b[:4], uint32(kind))
	h.Write(b[:5])
	if kind == ClipFiles {
		for _, f := range files {
			h.Write([]byte(f))
			h.Write([]byte{0})
		}
	} else {
		h.Write(data)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func (a *App) charCount(b []byte) int {
	if utf8.Valid(b) {
		return utf8.RuneCount(b)
	}
	return len(b)
}

func (a *App) osName() string { return platform.OSName() }
