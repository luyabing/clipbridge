package app

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type peer struct {
	ID         string
	Name       string
	OS         string
	Addr       string
	Secure     bool
	Discovered bool
	LastSeen   time.Time
	LastError  string
	Conn       *conn
}

type peerSnapshot struct {
	ID         string
	Name       string
	OS         string
	Addr       string
	Secure     bool
	Discovered bool
	LastSeen   time.Time
	LastError  string
	Connected  bool
	Primary    bool
	Direction  string
}

type hub struct {
	app *App

	ctx    context.Context
	cancel context.CancelFunc
	ln     net.Listener

	mu       sync.Mutex
	peers    map[string]*peer
	conns    map[*conn]struct{}
	lastDial map[string]time.Time
	dialing  map[string]bool
	closed   bool

	sem chan struct{}
	wg  sync.WaitGroup
}

func newHub(a *App) *hub {
	ctx, cancel := context.WithCancel(a.ctx)
	return &hub{
		app:      a,
		ctx:      ctx,
		cancel:   cancel,
		peers:    make(map[string]*peer),
		conns:    make(map[*conn]struct{}),
		lastDial: make(map[string]time.Time),
		dialing:  make(map[string]bool),
		sem:      make(chan struct{}, 32),
	}
}

// Start listens for inbound peers.
func (h *hub) Start() error {
	cfg := h.app.Config()
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(cfg.TCPPort))
	if err != nil {
		return fmt.Errorf("无法监听 TCP %d：%w", cfg.TCPPort, err)
	}
	h.ln = ln
	h.app.logf("剪贴板通道监听 TCP %d", cfg.TCPPort)
	h.goTrack(h.acceptLoop)
	return nil
}

func (h *hub) Shutdown() {
	h.cancel()
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	ln := h.ln
	conns := make([]*conn, 0, len(h.conns))
	for c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.Unlock()
	if ln != nil {
		ln.Close()
	}
	for _, c := range conns {
		c.close("程序退出")
	}
	h.wg.Wait()
}

func (h *hub) goTrack(fn func()) bool {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return false
	}
	h.wg.Add(1)
	h.mu.Unlock()
	go func() {
		defer h.wg.Done()
		fn()
	}()
	return true
}

func (h *hub) acceptLoop() {
	for {
		c, err := h.ln.Accept()
		if err != nil {
			if h.ctx.Err() != nil {
				return
			}
			h.app.debugf("接受连接失败: %v", err)
			continue
		}
		select {
		case h.sem <- struct{}{}:
		case <-h.ctx.Done():
			c.Close()
			return
		}
		h.goTrack(func() {
			defer func() { <-h.sem }()
			h.app.handleConn(c, false, "")
		})
	}
}

// DialManual dials a configured address even when auto-discovery is off.
func (h *hub) DialManual(addr string) {
	cfg := h.app.Config()
	addr, err := normalizePeerAddr(addr, cfg.TCPPort)
	if err != nil {
		h.app.debugf("手动地址无效 %q: %v", addr, err)
		return
	}
	h.dial(addr, "", true)
}

func (h *hub) dial(addr, expectedID string, force bool) {
	key := "m:" + addr
	if expectedID != "" {
		key = "id:" + expectedID
	}
	now := time.Now()
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	if h.dialing[key] {
		h.mu.Unlock()
		return
	}
	wait := 4 * time.Second
	if force {
		wait = 3 * time.Second
	}
	if last, ok := h.lastDial[key]; ok && now.Sub(last) < wait {
		h.mu.Unlock()
		return
	}
	if expectedID != "" {
		if p := h.peers[expectedID]; p != nil && p.Conn != nil {
			h.mu.Unlock()
			return
		}
	} else if h.connectedAddrLocked(addr) {
		h.mu.Unlock()
		return
	}
	h.dialing[key] = true
	h.lastDial[key] = now
	h.mu.Unlock()

	h.app.debugf("连接 %s", addr)
	h.goTrack(func() {
		defer func() {
			h.mu.Lock()
			delete(h.dialing, key)
			h.mu.Unlock()
		}()
		d := net.Dialer{Timeout: 4 * time.Second, KeepAlive: 30 * time.Second}
		c, err := d.DialContext(h.ctx, "tcp", addr)
		if err != nil {
			h.app.debugf("连接 %s 失败: %v", addr, err)
			return
		}
		h.app.handleConn(c, true, addr)
	})
}

func (h *hub) connectedAddrLocked(addr string) bool {
	for _, p := range h.peers {
		if p.Conn != nil && p.Conn.remoteAddr == addr {
			return true
		}
	}
	return false
}

// HasConnectedHost reports whether any active connection terminates at host.
func (h *hub) HasConnectedHost(host string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, p := range h.peers {
		if p.Conn == nil {
			continue
		}
		h, _, err := net.SplitHostPort(p.Conn.remoteAddr)
		if err == nil && h == host {
			return true
		}
	}
	return false
}

// OnDiscovery records a peer advertisement and optionally dials it.
func (h *hub) OnDiscovery(p discoveryPacket, ip string) {
	addr := net.JoinHostPort(ip, strconv.Itoa(p.Port))
	cfg := h.app.Config()
	secureMismatch := (cfg.PairCode != "") != p.Secure
	h.mu.Lock()
	pe := h.peers[p.ID]
	if pe == nil {
		pe = &peer{ID: p.ID}
		h.peers[p.ID] = pe
	}
	pe.Name = p.Name
	pe.OS = p.OS
	pe.Secure = p.Secure
	pe.Addr = addr
	pe.Discovered = true
	pe.LastSeen = time.Now()
	if secureMismatch {
		pe.LastError = "配对码设置不一致"
	} else if pe.LastError == "配对码设置不一致" {
		pe.LastError = ""
	}
	connected := pe.Conn != nil
	h.mu.Unlock()

	if secureMismatch || connected || !cfg.AutoConnect {
		return
	}
	h.dial(addr, p.ID, false)
}

// PeerError records a handshake/authentication failure for the UI.
func (h *hub) PeerError(id, name, addr, msg string) {
	h.mu.Lock()
	if id != "" {
		p := h.peers[id]
		if p == nil {
			p = &peer{ID: id}
			h.peers[id] = p
		}
		p.Name = name
		p.Addr = addr
		p.LastSeen = time.Now()
		p.LastError = msg
	} else if addr != "" {
		for _, p := range h.peers {
			if p.Addr == addr || (p.Conn != nil && p.Conn.remoteAddr == addr) {
				p.LastError = msg
				break
			}
		}
	}
	h.mu.Unlock()
	if msg != "" {
		display := name
		if display == "" {
			display = addr
		}
		if display == "" {
			display = "对方设备"
		}
		h.app.events.Add("sys", "error", fmt.Sprintf("无法与 %s 建立可信连接：%s", display, msg))
	}
}

// Touch updates liveness for an active peer.
func (h *hub) Touch(id string) {
	h.mu.Lock()
	if p := h.peers[id]; p != nil {
		p.LastSeen = time.Now()
	}
	h.mu.Unlock()
}

func (h *hub) addConn(c *conn) bool {
	h.mu.Lock()
	p := h.peers[c.peerID]
	if p == nil {
		p = &peer{ID: c.peerID}
		h.peers[c.peerID] = p
	}
	p.Name = c.peerName
	p.OS = c.peerOS
	p.Secure = c.secure
	p.Addr = c.remoteAddr
	p.LastSeen = time.Now()
	p.LastError = ""
	existing := p.Conn
	var replace *conn
	if existing == nil {
		p.Conn = c
	} else if c.primary && !existing.primary {
		p.Conn = c
		replace = existing
	} else {
		h.mu.Unlock()
		c.close("重复连接")
		return false
	}
	h.conns[c] = struct{}{}
	h.mu.Unlock()

	if replace != nil {
		replace.close("切换到优先连接")
		h.app.events.Add("sys", "conn", fmt.Sprintf("已建立与 %s 的优先连接", c.peerName))
	} else {
		h.app.events.Add("sys", "conn", fmt.Sprintf("已连接 %s（%s）", c.peerName, c.peerOS))
	}
	h.app.logf("已连接 %s @ %s", c.peerName, c.remoteAddr)
	h.app.syncTrayState()
	return true
}

func (h *hub) removeConn(c *conn) {
	h.mu.Lock()
	delete(h.conns, c)
	p := h.peers[c.peerID]
	wasCurrent := p != nil && p.Conn == c
	if wasCurrent {
		p.Conn = nil
		p.LastSeen = time.Now()
	}
	h.mu.Unlock()
	if wasCurrent {
		h.app.events.Add("sys", "conn", fmt.Sprintf("已断开与 %s 的连接", c.peerName))
		h.app.syncTrayState()
	}
}

func (h *hub) Snapshot() []peerSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	out := make([]peerSnapshot, 0, len(h.peers))
	for _, p := range h.peers {
		if p.Conn == nil && !p.Discovered && now.Sub(p.LastSeen) > time.Minute {
			continue
		}
		v := peerSnapshot{
			ID:         p.ID,
			Name:       p.Name,
			OS:         p.OS,
			Addr:       p.Addr,
			Secure:     p.Secure,
			Discovered: p.Discovered,
			LastSeen:   p.LastSeen,
			LastError:  p.LastError,
		}
		if p.Conn != nil {
			v.Connected = true
			v.Primary = p.Conn.primary
			v.Direction = p.Conn.direction()
			v.Addr = p.Conn.remoteAddr
			v.LastSeen = now
			v.Secure = p.Conn.secure
		}
		out = append(out, v)
	}
	return out
}

func (h *hub) Broadcast(typ byte, payload []byte) int {
	h.mu.Lock()
	conns := make([]*conn, 0, len(h.conns))
	for c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.Unlock()
	n := 0
	for _, c := range conns {
		if c.TrySend(typ, payload) {
			n++
		}
	}
	return n
}

func (h *hub) CloseAll(reason string) {
	h.mu.Lock()
	conns := make([]*conn, 0, len(h.conns))
	for c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.Unlock()
	for _, c := range conns {
		c.close(reason)
	}
}

// maintenanceGC drops stale discovered peers and dial timestamps.
func (h *hub) maintenanceGC() {
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, p := range h.peers {
		if p.Conn == nil && !p.Discovered && now.Sub(p.LastSeen) > time.Minute {
			delete(h.peers, id)
		}
		if p.Conn == nil && p.Discovered && now.Sub(p.LastSeen) > 2*time.Minute {
			delete(h.peers, id)
		}
	}
	for k, t := range h.lastDial {
		if now.Sub(t) > 2*time.Minute {
			delete(h.lastDial, k)
		}
	}
}

func normalizePeerAddr(addr string, defaultPort int) (string, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", fmt.Errorf("地址为空")
	}
	if host, port, err := net.SplitHostPort(addr); err == nil {
		if host == "" {
			return "", fmt.Errorf("地址格式错误")
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("端口必须是 1–65535 之间的数字")
		}
		return net.JoinHostPort(host, strconv.Itoa(n)), nil
	}
	if strings.HasPrefix(addr, "[") && strings.HasSuffix(addr, "]") {
		host := strings.TrimSpace(addr[1 : len(addr)-1])
		if host == "" {
			return "", fmt.Errorf("地址格式错误")
		}
		return net.JoinHostPort(host, strconv.Itoa(defaultPort)), nil
	}
	if strings.Count(addr, ":") > 1 {
		// Bare IPv6 address.
		return net.JoinHostPort(addr, strconv.Itoa(defaultPort)), nil
	}
	if strings.Contains(addr, ":") {
		return "", fmt.Errorf("地址和端口格式错误")
	}
	if strings.ContainsAny(addr, " /\\") {
		return "", fmt.Errorf("地址格式错误")
	}
	return net.JoinHostPort(addr, strconv.Itoa(defaultPort)), nil
}
