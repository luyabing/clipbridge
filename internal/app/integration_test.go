package app

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"clipbridge/internal/config"
)

type clipWrite struct {
	kind  ClipKind
	data  []byte
	files []string
}

// fakeClip implements Clipboard without touching the real desktop clipboard.
// Writes are recorded and also pushed through the watcher, just like a real
// clipboard manager would echo our own write back to the app.
type fakeClip struct {
	mu     sync.Mutex
	writes []clipWrite
	events chan ClipEvent
}

func newFakeClip() *fakeClip {
	return &fakeClip{events: make(chan ClipEvent, 64)}
}

func (f *fakeClip) Watch(ctx context.Context) <-chan ClipEvent {
	out := make(chan ClipEvent, 16)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-f.events:
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

func (f *fakeClip) Write(_ context.Context, kind ClipKind, data []byte, files []string) error {
	w := clipWrite{
		kind:  kind,
		data:  append([]byte(nil), data...),
		files: append([]string(nil), files...),
	}
	f.mu.Lock()
	f.writes = append(f.writes, w)
	f.mu.Unlock()
	f.push(ClipEvent{Kind: kind, Data: w.data, Files: w.files})
	return nil
}

func (f *fakeClip) push(ev ClipEvent) {
	ev.Data = append([]byte(nil), ev.Data...)
	ev.Files = append([]string(nil), ev.Files...)
	select {
	case f.events <- ev:
	default:
	}
}

func (f *fakeClip) waitWrite(t *testing.T, kind ClipKind, what string) clipWrite {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		for _, w := range f.writes {
			if w.kind == kind {
				f.mu.Unlock()
				return w
			}
		}
		f.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等待%s超时", what)
	return clipWrite{}
}

func (f *fakeClip) count(kind ClipKind) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, w := range f.writes {
		if w.kind == kind {
			n++
		}
	}
	return n
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	conn.Close()
	return port
}

func distinctPorts(t *testing.T) (tcp, web, discovery int) {
	t.Helper()
	tcp = freeTCPPort(t)
	web = freeTCPPort(t)
	for web == tcp {
		web = freeTCPPort(t)
	}
	discovery = freeUDPPort(t)
	for discovery == tcp || discovery == web {
		discovery = freeUDPPort(t)
	}
	return tcp, web, discovery
}

func startTestApp(t *testing.T, name, pairCode string, auto bool) (*App, *fakeClip) {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.DeviceName = name
	cfg.PairCode = pairCode
	cfg.AutoConnect = auto
	cfg.TCPPort, cfg.WebPort, cfg.DiscoveryPort = distinctPorts(t)
	cfg.DownloadDir = filepath.Join(dir, "downloads")
	cfg.ManualPeers = []string{}
	path := filepath.Join(dir, "config.json")
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}

	clip := newFakeClip()
	a, err := New(Options{
		ConfigPath: path,
		Version:    "test",
		Headless:   true,
		Clipboard:  clip,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(); err != nil {
		a.Shutdown()
		t.Fatal(err)
	}
	t.Cleanup(a.Shutdown)
	return a, clip
}

func connectedCount(a *App) int {
	if a.hub == nil {
		return 0
	}
	n := 0
	for _, p := range a.hub.Snapshot() {
		if p.Connected {
			n++
		}
	}
	return n
}

func connectedPeer(t *testing.T, a *App) peerSnapshot {
	t.Helper()
	for _, p := range a.hub.Snapshot() {
		if p.Connected {
			return p
		}
	}
	t.Fatal("没有已连接的设备")
	return peerSnapshot{}
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等待%s超时", what)
}

func dial(t *testing.T, a, b *App) {
	t.Helper()
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(b.Config().TCPPort))
	a.hub.DialManual(addr)
	waitFor(t, 8*time.Second, "双方建立连接", func() bool {
		return connectedCount(a) == 1 && connectedCount(b) == 1
	})
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 30), G: uint8(y * 30), B: 180, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSyncTextImageAndFiles(t *testing.T) {
	a, clipA := startTestApp(t, "测试机 A", "", false)
	b, clipB := startTestApp(t, "测试机 B", "", false)
	dial(t, a, b)

	// Text.
	text := []byte("你好，剪贴桥！\n第二行")
	clipA.push(ClipEvent{Kind: ClipText, Data: text})
	w := clipB.waitWrite(t, ClipText, "接收文本")
	if !bytes.Equal(w.data, text) {
		t.Fatalf("text mismatch: %q", w.data)
	}
	if got := b.Status().Stats.Received; got < 1 {
		t.Fatalf("received counter = %d", got)
	}

	// Image.
	img := testPNG(t)
	clipA.push(ClipEvent{Kind: ClipImage, Data: img})
	w = clipB.waitWrite(t, ClipImage, "接收图片")
	if !bytes.Equal(w.data, img) {
		t.Fatal("image bytes mismatch")
	}

	// Files: large enough to span multiple 256 KiB chunks.
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "测试 文件.bin")
	content := bytes.Repeat([]byte("clipbridge-file-payload-"), 50000)
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatal(err)
	}
	clipA.push(ClipEvent{Kind: ClipFiles, Files: []string{src}})
	w = clipB.waitWrite(t, ClipFiles, "接收文件")
	if len(w.files) != 1 {
		t.Fatalf("received %d files", len(w.files))
	}
	got, err := os.ReadFile(w.files[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("file content mismatch: got %d bytes, want %d", len(got), len(content))
	}
	if !strings.HasPrefix(w.files[0], b.downloadRoot+string(os.PathSeparator)) {
		t.Fatalf("file saved outside download root: %s", w.files[0])
	}
	if filepath.Base(w.files[0]) != "测试 文件.bin" {
		t.Fatalf("file name = %q", filepath.Base(w.files[0]))
	}

	// Echo suppression: our fake clipboard echoes the writes above, and B
	// must not send them back to A.
	time.Sleep(600 * time.Millisecond)
	if n := clipA.count(ClipText); n != 0 {
		t.Fatalf("A received %d echoed text writes", n)
	}
	if n := a.Status().Stats.Received; n != 0 {
		t.Fatalf("A received counter = %d, want 0", n)
	}
}

func TestEncryptedTextSync(t *testing.T) {
	code := "测试配对码-2026"
	a, clipA := startTestApp(t, "加密 A", code, false)
	b, clipB := startTestApp(t, "加密 B", code, false)
	dial(t, a, b)

	pa := connectedPeer(t, a)
	pb := connectedPeer(t, b)
	if !pa.Secure || !pb.Secure {
		t.Fatalf("connection is not encrypted: %+v %+v", pa, pb)
	}

	text := []byte("这条消息应当加密传输")
	clipA.push(ClipEvent{Kind: ClipText, Data: text})
	w := clipB.waitWrite(t, ClipText, "接收加密文本")
	if !bytes.Equal(w.data, text) {
		t.Fatalf("text mismatch: %q", w.data)
	}
}

func TestSensitiveDisabledAndPaused(t *testing.T) {
	a, clipA := startTestApp(t, "过滤 A", "", false)
	b, clipB := startTestApp(t, "过滤 B", "", false)
	dial(t, a, b)

	// Password-manager content must not leave A.
	clipA.push(ClipEvent{Kind: ClipText, Data: []byte("password-123"), Sensitive: true})
	time.Sleep(time.Second)
	if n := clipB.count(ClipText); n != 0 {
		t.Fatalf("sensitive text was delivered (%d writes)", n)
	}

	// Text sync disabled on the receiving side.
	if err := b.UpdateConfig(func(c *config.Config) error {
		c.SyncText = false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	clipA.push(ClipEvent{Kind: ClipText, Data: []byte("disabled-text")})
	time.Sleep(time.Second)
	if n := clipB.count(ClipText); n != 0 {
		t.Fatalf("text was delivered while disabled (%d writes)", n)
	}

	// Re-enable and receive normally.
	if err := b.UpdateConfig(func(c *config.Config) error {
		c.SyncText = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	clipA.push(ClipEvent{Kind: ClipText, Data: []byte("enabled-text")})
	w := clipB.waitWrite(t, ClipText, "恢复后的文本")
	if string(w.data) != "enabled-text" {
		t.Fatalf("text = %q", w.data)
	}
	baseline := clipB.count(ClipText)

	// Pausing the sender stops clipboard traffic but keeps the connection.
	if err := a.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	clipA.push(ClipEvent{Kind: ClipText, Data: []byte("paused-text")})
	time.Sleep(800 * time.Millisecond)
	if n := clipB.count(ClipText); n != baseline {
		t.Fatalf("paused sender delivered text (%d -> %d)", baseline, n)
	}
	if err := a.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	clipA.push(ClipEvent{Kind: ClipText, Data: []byte("resumed-text")})
	waitFor(t, 3*time.Second, "收到恢复后的文本", func() bool {
		clipB.mu.Lock()
		defer clipB.mu.Unlock()
		for _, w := range clipB.writes {
			if w.kind == ClipText && string(w.data) == "resumed-text" {
				return true
			}
		}
		return false
	})
}

func TestPairCodeMismatch(t *testing.T) {
	cases := []struct {
		name   string
		codeA  string
		codeB  string
		expect string
	}{
		{"不同配对码", "code-one", "code-two", "配对码不一致"},
		{"一端未设置", "", "code-two", "配对码设置不一致"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := startTestApp(t, "不匹配 A", tc.codeA, false)
			b, _ := startTestApp(t, "不匹配 B", tc.codeB, false)
			addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(b.Config().TCPPort))
			a.hub.DialManual(addr)

			waitFor(t, 8*time.Second, "记录配对失败", func() bool {
				for _, p := range a.hub.Snapshot() {
					if strings.Contains(p.LastError, tc.expect) {
						return true
					}
				}
				for _, ev := range a.Status().Events {
					if strings.Contains(ev.Message, tc.expect) {
						return true
					}
				}
				return false
			})
			if n := connectedCount(a); n != 0 {
				t.Fatalf("A has %d connections", n)
			}
			if n := connectedCount(b); n != 0 {
				t.Fatalf("B has %d connections", n)
			}
		})
	}
}

func TestSimultaneousDialsKeepOneConnection(t *testing.T) {
	a, clipA := startTestApp(t, "同时连接 A", "", false)
	b, clipB := startTestApp(t, "同时连接 B", "", false)
	addrA := net.JoinHostPort("127.0.0.1", strconv.Itoa(a.Config().TCPPort))
	addrB := net.JoinHostPort("127.0.0.1", strconv.Itoa(b.Config().TCPPort))

	go a.hub.DialManual(addrB)
	go b.hub.DialManual(addrA)

	waitFor(t, 10*time.Second, "同时拨号后保持单条连接", func() bool {
		return connectedCount(a) == 1 && connectedCount(b) == 1
	})
	time.Sleep(500 * time.Millisecond)
	if n := connectedCount(a); n != 1 {
		t.Fatalf("A has %d connections", n)
	}
	if n := connectedCount(b); n != 1 {
		t.Fatalf("B has %d connections", n)
	}

	// The connection initiated by the smaller device ID must win on both ends.
	wantA := "out"
	if a.Config().DeviceID > b.Config().DeviceID {
		wantA = "in"
	}
	if got := connectedPeer(t, a).Direction; got != wantA {
		t.Fatalf("A primary direction = %q, want %q", got, wantA)
	}
	wantB := "in"
	if wantA == "in" {
		wantB = "out"
	}
	if got := connectedPeer(t, b).Direction; got != wantB {
		t.Fatalf("B primary direction = %q, want %q", got, wantB)
	}

	// And the surviving connection still carries clipboard data.
	clipA.push(ClipEvent{Kind: ClipText, Data: []byte("同时拨号后仍可同步")})
	w := clipB.waitWrite(t, ClipText, "接收文本")
	if string(w.data) != "同时拨号后仍可同步" {
		t.Fatalf("text = %q", w.data)
	}
}
