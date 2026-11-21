package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"

	"clipbridge/internal/proto"
)

type discoveryPacket struct {
	Magic      string `json:"magic"`
	Version    int    `json:"v"`
	AppVersion string `json:"appVersion,omitempty"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	OS         string `json:"os"`
	Port       int    `json:"port"`
	Secure     bool   `json:"secure"`
}

type discovery struct {
	app     *App
	conn    *net.UDPConn
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	trigger chan struct{}
}

func newDiscovery(a *App) *discovery {
	ctx, cancel := context.WithCancel(a.ctx)
	return &discovery{
		app:     a,
		ctx:     ctx,
		cancel:  cancel,
		trigger: make(chan struct{}, 1),
	}
}

// Start begins advertising and listening. Discovery failures are non-fatal:
// manual IP connections still work.
func (d *discovery) Start() {
	cfg := d.app.Config()
	addr := &net.UDPAddr{IP: net.IPv4zero, Port: cfg.DiscoveryPort}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		d.app.logf("UDP 自动发现启动失败: %v", err)
		d.app.events.Add("sys", "error", fmt.Sprintf("自动发现不可用（UDP %d），请用 IP 手动连接", cfg.DiscoveryPort))
		return
	}
	d.conn = conn
	_ = conn.SetReadBuffer(1 << 20)
	d.app.logf("自动发现监听 UDP %d", cfg.DiscoveryPort)
	d.wg.Add(2)
	go d.readLoop()
	go d.broadcastLoop()
}

func (d *discovery) Shutdown() {
	d.cancel()
	if d.conn != nil {
		d.conn.Close()
	}
	d.wg.Wait()
}

func (d *discovery) Trigger() {
	select {
	case d.trigger <- struct{}{}:
	default:
	}
}

func (d *discovery) packet() ([]byte, error) {
	cfg := d.app.Config()
	p := discoveryPacket{
		Magic:      proto.Magic,
		Version:    proto.Version,
		AppVersion: d.app.version,
		ID:         cfg.DeviceID,
		Name:       cfg.DeviceName,
		OS:         d.app.osName(),
		Port:       cfg.TCPPort,
		Secure:     cfg.PairCode != "",
	}
	return json.Marshal(p)
}

func (d *discovery) broadcastLoop() {
	defer d.wg.Done()
	d.send()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			d.send()
		case <-d.trigger:
			d.send()
		}
	}
}

func (d *discovery) send() {
	if d.conn == nil {
		return
	}
	payload, err := d.packet()
	if err != nil {
		return
	}
	for _, ip := range broadcastIPs() {
		addr := &net.UDPAddr{IP: ip, Port: d.app.Config().DiscoveryPort}
		_ = d.conn.SetWriteDeadline(time.Now().Add(time.Second))
		if _, err := d.conn.WriteToUDP(payload, addr); err != nil {
			d.app.debugf("广播到 %s 失败: %v", ip, err)
		}
	}
}

func (d *discovery) readLoop() {
	defer d.wg.Done()
	buf := make([]byte, 64*1024)
	for {
		n, src, err := d.conn.ReadFromUDP(buf)
		if err != nil {
			if d.ctx.Err() != nil {
				return
			}
			d.app.debugf("UDP 读取失败: %v", err)
			continue
		}
		var p discoveryPacket
		if err := json.Unmarshal(buf[:n], &p); err != nil {
			continue
		}
		if p.Magic != proto.Magic || p.Version != proto.Version {
			continue
		}
		if p.ID == "" || len(p.ID) > 128 || p.Name == "" || len(p.Name) > 200 {
			continue
		}
		if p.Port < 1 || p.Port > 65535 {
			continue
		}
		cfg := d.app.Config()
		if p.ID == cfg.DeviceID {
			continue
		}
		if ip4 := src.IP.To4(); ip4 == nil {
			continue
		}
		d.app.hub.OnDiscovery(p, src.IP.String())
	}
}

func broadcastIPs() []net.IP {
	var out []net.IP
	seen := map[string]bool{}
	add := func(ip net.IP) {
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
			return
		}
		s := ip.String()
		if !seen[s] {
			seen[s] = true
			out = append(out, ip)
		}
	}
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			mask := ipnet.Mask
			if len(mask) == 16 {
				mask = mask[12:]
			}
			if ip == nil || len(mask) != 4 {
				continue
			}
			b := make(net.IP, 4)
			for i := 0; i < 4; i++ {
				b[i] = ip[i] | ^mask[i]
			}
			add(b)
		}
	}
	add(net.IPv4bcast)
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}
