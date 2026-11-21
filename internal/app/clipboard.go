package app

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/png"
	"log"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"golang.design/x/clipboard"
)

const (
	// MaxTextBytes is the largest clipboard text we exchange.
	MaxTextBytes = 16 << 20
	// MaxImageBytes is the largest PNG image we exchange.
	MaxImageBytes = 64 << 20
	// MaxImagePixels guards against decompression bombs.
	MaxImagePixels = 120_000_000
)

// ClipKind identifies a clipboard representation.
type ClipKind int

const (
	ClipText ClipKind = iota
	ClipImage
	ClipFiles
)

func (k ClipKind) String() string {
	switch k {
	case ClipText:
		return "text"
	case ClipImage:
		return "image"
	case ClipFiles:
		return "files"
	default:
		return "unknown"
	}
}

// ClipEvent is one clipboard change reported by the OS backend.
type ClipEvent struct {
	Kind      ClipKind
	Data      []byte
	Files     []string
	Sensitive bool
}

// Clipboard is the OS clipboard abstraction. It is an interface so the
// networking layer can be tested without touching a real desktop clipboard.
type Clipboard interface {
	Watch(ctx context.Context) <-chan ClipEvent
	Write(ctx context.Context, kind ClipKind, data []byte, files []string) error
}

type systemClipboard struct {
	debug  bool
	logger *log.Logger
}

// NewSystemClipboard initializes the native clipboard backend.
func NewSystemClipboard(debug bool, logger *log.Logger) (Clipboard, error) {
	if err := clipboard.Init(); err != nil {
		return nil, fmt.Errorf("无法访问系统剪贴板：%w", err)
	}
	return systemClipboard{debug: debug, logger: logger}, nil
}

func (c systemClipboard) debugf(format string, args ...any) {
	if c.debug && c.logger != nil {
		c.logger.Printf("DEBUG "+format, args...)
	}
}

func (c systemClipboard) Watch(ctx context.Context) <-chan ClipEvent {
	in := clipboard.Watch(ctx)
	out := make(chan ClipEvent, 16)
	go func() {
		defer close(out)
		c.debugf("系统剪贴板监听已启动")
		defer c.debugf("系统剪贴板监听已停止")
		for d := range in {
			ev := ClipEvent{Sensitive: d.Sensitive}
			switch d.Format {
			case clipboard.FmtText:
				ev.Kind = ClipText
				ev.Data = d.Bytes
			case clipboard.FmtImage:
				ev.Kind = ClipImage
				ev.Data = d.Bytes
			case clipboard.FmtFiles:
				ev.Kind = ClipFiles
				ev.Files = ParseURIList(string(d.Bytes))
				ev.Data = d.Bytes
			default:
				continue
			}
			c.debugf("读取到系统剪贴板事件：类型=%s 大小=%d 敏感=%v", ev.Kind, len(ev.Data), ev.Sensitive)
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

func (systemClipboard) Write(ctx context.Context, kind ClipKind, data []byte, files []string) error {
	switch kind {
	case ClipText:
		_, err := clipboard.Write(ctx, clipboard.FmtText, data)
		return err
	case ClipImage:
		_, err := clipboard.Write(ctx, clipboard.FmtImage, data)
		return err
	case ClipFiles:
		if len(files) == 0 {
			return errors.New("没有可写入剪贴板的文件")
		}
		_, err := clipboard.WriteFiles(ctx, files)
		return err
	default:
		return fmt.Errorf("未知剪贴板类型 %d", kind)
	}
}

// ParseURIList parses the text/uri-list representation used for file lists.
// It also tolerates applications that put one absolute path per line.
func ParseURIList(s string) []string {
	var out []string
	seen := make(map[string]bool)
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p := line
		if strings.HasPrefix(line, "file:") {
			u, err := url.Parse(line)
			if err != nil {
				continue
			}
			if u.Scheme != "file" || (u.Host != "" && !strings.EqualFold(u.Host, "localhost")) {
				continue
			}
			p = u.Path
			if p == "" {
				continue
			}
			if runtime.GOOS == "windows" {
				p = strings.TrimPrefix(p, "/")
				if len(p) >= 2 && p[1] == ':' {
					// C:/path -> C:\path
				} else {
					continue
				}
			}
			p = filepath.FromSlash(p)
		} else if !filepath.IsAbs(p) {
			continue
		}
		p = filepath.Clean(p)
		if !filepath.IsAbs(p) || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// ValidateText rejects oversized or invalid UTF-8 clipboard text.
func ValidateText(b []byte) error {
	if len(b) > MaxTextBytes {
		return fmt.Errorf("文本过大（%s，上限 %s）", humanBytes(int64(len(b))), humanBytes(MaxTextBytes))
	}
	if !utf8.Valid(b) {
		return errors.New("文本不是有效 UTF-8")
	}
	return nil
}

// ValidatePNG checks size, signature, and decoded dimensions.
func ValidatePNG(b []byte) error {
	if len(b) > MaxImageBytes {
		return fmt.Errorf("图片过大（%s，上限 %s）", humanBytes(int64(len(b))), humanBytes(MaxImageBytes))
	}
	if len(b) < 8 || !bytes.Equal(b[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		return errors.New("图片不是 PNG 格式")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("无法解析图片：%w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return errors.New("图片尺寸无效")
	}
	if int64(cfg.Width)*int64(cfg.Height) > MaxImagePixels {
		return fmt.Errorf("图片分辨率过大（%d × %d）", cfg.Width, cfg.Height)
	}
	return nil
}

func humanBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	f := float64(n)
	for _, u := range units {
		f /= 1024
		if f < 1024 {
			return fmt.Sprintf("%.1f %s", f, u)
		}
	}
	return fmt.Sprintf("%.1f PB", f/1024)
}
