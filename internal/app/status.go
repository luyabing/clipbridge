package app

import (
	"os"
	"sort"
	"time"

	"clipbridge/internal/config"
)

// PeerView is the UI-facing view of one known peer.
type PeerView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	OS         string `json:"os"`
	Address    string `json:"address"`
	Secure     bool   `json:"secure"`
	Connected  bool   `json:"connected"`
	Primary    bool   `json:"primary"`
	Discovered bool   `json:"discovered"`
	LastSeen   int64  `json:"lastSeen"`
	LastError  string `json:"lastError,omitempty"`
	Direction  string `json:"direction,omitempty"`
}

// StatsView reports cumulative clipboard transfers.
type StatsView struct {
	Sent     int64 `json:"sent"`
	Received int64 `json:"received"`
	BytesOut int64 `json:"bytesOut"`
	BytesIn  int64 `json:"bytesIn"`
}

// PortsView lists the three listening ports.
type PortsView struct {
	TCP       int `json:"tcp"`
	Discovery int `json:"discovery"`
	Web       int `json:"web"`
}

// Status is returned by GET /api/status and drives the control panel.
type Status struct {
	App            string        `json:"app"`
	Name           string        `json:"name"`
	Version        string        `json:"version"`
	OS             string        `json:"os"`
	DeviceID       string        `json:"deviceId"`
	DeviceName     string        `json:"deviceName"`
	Hostname       string        `json:"hostname"`
	LocalIPs       []string      `json:"localIPs"`
	WebURL         string        `json:"webUrl"`
	Ports          PortsView     `json:"ports"`
	Secure         bool          `json:"secure"`
	Paused         bool          `json:"paused"`
	Headless       bool          `json:"headless"`
	StartedAt      int64         `json:"startedAt"`
	UptimeSeconds  int64         `json:"uptimeSeconds"`
	Connected      []PeerView    `json:"connected"`
	Nearby         []PeerView    `json:"nearby"`
	ManualPeers    []string      `json:"manualPeers"`
	Config         config.Config `json:"config"`
	Autostart      bool          `json:"autostart"`
	AutostartError string        `json:"autostartError,omitempty"`
	DownloadDir    string        `json:"downloadDir"`
	ConfigPath     string        `json:"configPath"`
	LogPath        string        `json:"logPath"`
	Stats          StatsView     `json:"stats"`
	Transfer       transferView  `json:"transfer"`
	Events         []Event       `json:"events"`
}

// Status builds a point-in-time snapshot for the control panel.
func (a *App) Status() Status {
	cfg := a.Config()
	autostart, autoErr := a.AutostartStatus()
	st := Status{
		App:           "clipbridge",
		Name:          "剪贴桥",
		Version:       a.version,
		OS:            a.osName(),
		DeviceID:      cfg.DeviceID,
		DeviceName:    cfg.DeviceName,
		LocalIPs:      append([]string(nil), a.localIPs...),
		WebURL:        a.URL(),
		Ports:         PortsView{TCP: cfg.TCPPort, Discovery: cfg.DiscoveryPort, Web: cfg.WebPort},
		Secure:        cfg.PairCode != "",
		Paused:        cfg.Paused,
		Headless:      a.headless,
		StartedAt:     a.startedAt.UnixMilli(),
		UptimeSeconds: int64(time.Since(a.startedAt).Seconds()),
		ManualPeers:   append([]string(nil), cfg.ManualPeers...),
		Config:        cfg,
		Autostart:     autostart,
		DownloadDir:   a.downloadRoot,
		ConfigPath:    a.cfgPath,
		LogPath:       a.logPath,
		Stats: StatsView{
			Sent:     a.stats.sent.Load(),
			Received: a.stats.received.Load(),
			BytesOut: a.stats.bytesOut.Load(),
			BytesIn:  a.stats.bytesIn.Load(),
		},
		Transfer: a.transfer.snapshot(),
		Events:   a.events.Snapshot(),
	}
	if host, err := os.Hostname(); err == nil {
		st.Hostname = host
	}
	if autoErr != nil {
		st.AutostartError = autoErr.Error()
	}
	if a.hub != nil {
		now := time.Now()
		for _, p := range a.hub.Snapshot() {
			v := PeerView{
				ID:         p.ID,
				Name:       p.Name,
				OS:         p.OS,
				Address:    p.Addr,
				Secure:     p.Secure,
				Connected:  p.Connected,
				Primary:    p.Primary,
				Discovered: p.Discovered,
				LastSeen:   p.LastSeen.UnixMilli(),
				LastError:  p.LastError,
				Direction:  p.Direction,
			}
			if p.Connected {
				st.Connected = append(st.Connected, v)
			} else if p.Discovered && now.Sub(p.LastSeen) < 20*time.Second {
				st.Nearby = append(st.Nearby, v)
			}
		}
	}
	sort.Slice(st.Connected, func(i, j int) bool {
		if st.Connected[i].Name == st.Connected[j].Name {
			return st.Connected[i].ID < st.Connected[j].ID
		}
		return st.Connected[i].Name < st.Connected[j].Name
	})
	sort.Slice(st.Nearby, func(i, j int) bool {
		return st.Nearby[i].Name < st.Nearby[j].Name
	})
	return st
}
