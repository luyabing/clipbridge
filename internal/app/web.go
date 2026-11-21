package app

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"clipbridge/internal/assets"
	"clipbridge/internal/config"
)

//go:embed web/index.html web/app.js web/style.css
var webFS embed.FS

var (
	indexHTML []byte
	appJS     []byte
	styleCSS  []byte
)

func init() {
	indexHTML, _ = webFS.ReadFile("web/index.html")
	appJS, _ = webFS.ReadFile("web/app.js")
	styleCSS, _ = webFS.ReadFile("web/style.css")
}

type webServer struct {
	srv *http.Server
	ln  net.Listener
	app *App
}

func (a *App) startWeb() error {
	cfg := a.Config()
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.WebPort))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if a.probeExistingInstance() {
			return ErrAlreadyRunning
		}
		return fmt.Errorf("无法监听控制面板 %s：%w", addr, err)
	}
	mux := http.NewServeMux()
	a.routes(mux)
	srv := &http.Server{
		Handler:           a.webMiddleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	a.web = &webServer{srv: srv, ln: ln, app: a}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			a.logf("控制面板停止: %v", err)
		}
	}()
	return nil
}

func (w *webServer) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = w.srv.Shutdown(ctx)
	_ = w.ln.Close()
}

func (a *App) webMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := a.Config()
		if !hostAllowed(r.Host, cfg.WebPort) {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}

func hostAllowed(host string, port int) bool {
	if host == "" {
		return false
	}
	h, p, err := net.SplitHostPort(host)
	if err != nil {
		return false
	}
	if p != strconv.Itoa(port) {
		return false
	}
	return h == "127.0.0.1" || h == "localhost"
}

func (a *App) routes(mux *http.ServeMux) {
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeBytes(w, "text/html; charset=utf-8", indexHTML)
	})
	mux.HandleFunc("/app.js", func(w http.ResponseWriter, r *http.Request) {
		writeBytes(w, "text/javascript; charset=utf-8", appJS)
	})
	mux.HandleFunc("/style.css", func(w http.ResponseWriter, r *http.Request) {
		writeBytes(w, "text/css; charset=utf-8", styleCSS)
	})
	mux.HandleFunc("/icon.png", func(w http.ResponseWriter, r *http.Request) {
		writeBytes(w, "image/png", assets.IconPNG)
	})
	mux.HandleFunc("/api/status", a.handleStatus)
	mux.HandleFunc("/api/config", a.handleConfig)
	mux.HandleFunc("/api/pause", a.handlePause)
	mux.HandleFunc("/api/peer", a.handlePeer)
	mux.HandleFunc("/api/open", a.handleOpen)
	mux.HandleFunc("/api/quit", a.handleQuit)
}

func writeBytes(w http.ResponseWriter, ctype string, b []byte) {
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	_, _ = w.Write(b)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, a.Status())
}

type configPatch struct {
	DeviceName    *string `json:"deviceName"`
	PairCode      *string `json:"pairCode"`
	AutoConnect   *bool   `json:"autoConnect"`
	SyncText      *bool   `json:"syncText"`
	SyncImage     *bool   `json:"syncImage"`
	SyncFiles     *bool   `json:"syncFiles"`
	SkipSensitive *bool   `json:"skipSensitive"`
	Paused        *bool   `json:"paused"`
	Autostart     *bool   `json:"autostart"`
}

func (a *App) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !validMutation(r) {
		writeErr(w, http.StatusForbidden, fmt.Errorf("请求来源不受信任"))
		return
	}
	var p configPatch
	if err := decodeJSON(w, r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	err := a.UpdateConfig(func(c *config.Config) error {
		if p.DeviceName != nil {
			c.DeviceName = *p.DeviceName
		}
		if p.PairCode != nil {
			c.PairCode = *p.PairCode
		}
		if p.AutoConnect != nil {
			c.AutoConnect = *p.AutoConnect
		}
		if p.SyncText != nil {
			c.SyncText = *p.SyncText
		}
		if p.SyncImage != nil {
			c.SyncImage = *p.SyncImage
		}
		if p.SyncFiles != nil {
			c.SyncFiles = *p.SyncFiles
		}
		if p.SkipSensitive != nil {
			c.SkipSensitive = *p.SkipSensitive
		}
		if p.Paused != nil {
			c.Paused = *p.Paused
		}
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if p.Autostart != nil {
		if err := a.SetAutostart(*p.Autostart); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, a.Status())
}

func (a *App) handlePause(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !validMutation(r) {
		writeErr(w, http.StatusForbidden, fmt.Errorf("请求来源不受信任"))
		return
	}
	var req struct {
		Paused *bool `json:"paused"`
	}
	if err := decodeJSON(w, r, &req); err != nil || req.Paused == nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("缺少 paused 参数"))
		return
	}
	if err := a.SetPaused(*req.Paused); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, a.Status())
}

func (a *App) handlePeer(w http.ResponseWriter, r *http.Request) {
	if !validMutation(r) {
		writeErr(w, http.StatusForbidden, fmt.Errorf("请求来源不受信任"))
		return
	}
	var req struct {
		Addr string `json:"addr"`
	}
	switch r.Method {
	case http.MethodPost:
		if err := decodeJSON(w, r, &req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		cfg := a.Config()
		norm, err := normalizePeerAddr(req.Addr, cfg.TCPPort)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		err = a.UpdateConfig(func(c *config.Config) error {
			for _, existing := range c.ManualPeers {
				if strings.EqualFold(existing, norm) {
					return nil
				}
			}
			c.ManualPeers = append(c.ManualPeers, norm)
			return nil
		})
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if a.hub != nil {
			a.hub.DialManual(norm)
		}
		writeJSON(w, http.StatusOK, a.Status())
	case http.MethodDelete:
		if err := decodeJSON(w, r, &req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		cfg := a.Config()
		norm, err := normalizePeerAddr(req.Addr, cfg.TCPPort)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		err = a.UpdateConfig(func(c *config.Config) error {
			out := c.ManualPeers[:0]
			for _, existing := range c.ManualPeers {
				if !strings.EqualFold(existing, norm) {
					out = append(out, existing)
				}
			}
			c.ManualPeers = out
			return nil
		})
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, a.Status())
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *App) handleOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !validMutation(r) {
		writeErr(w, http.StatusForbidden, fmt.Errorf("请求来源不受信任"))
		return
	}
	var req struct {
		Target string `json:"target"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	switch req.Target {
	case "downloads":
		_ = os.MkdirAll(a.downloadRoot, 0o755)
		a.openPath(a.downloadRoot)
	case "config":
		a.openPath(a.cfgDir)
	default:
		writeErr(w, http.StatusBadRequest, fmt.Errorf("未知目标"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) handleQuit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !validMutation(r) {
		writeErr(w, http.StatusForbidden, fmt.Errorf("请求来源不受信任"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	go func() {
		time.Sleep(200 * time.Millisecond)
		a.Shutdown()
	}()
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("请求格式错误：%w", err)
	}
	return nil
}

// validMutation blocks cross-site form posts and requires an explicit header
// that a browser cannot send cross-origin without a CORS preflight.
func validMutation(r *http.Request) bool {
	if r.Header.Get("X-ClipBridge") != "1" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	port := u.Port()
	if host != "127.0.0.1" && host != "localhost" {
		return false
	}
	if port == "" {
		port = "80"
	}
	return port == strconv.Itoa(configPort(r))
}

func configPort(r *http.Request) int {
	_, p, err := net.SplitHostPort(r.Host)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(p)
	return n
}
