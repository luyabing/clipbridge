package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"clipbridge/internal/proto"
)

const maxFilesPerTransfer = 200

type outgoingFile struct {
	Path string
	Name string
	Size int64
}

// collectOutgoingFiles validates a local file list before sending it.
func (a *App) collectOutgoingFiles(paths []string, maxMB int) ([]outgoingFile, int64, error) {
	if len(paths) == 0 {
		return nil, 0, errors.New("没有文件")
	}
	maxTotal := int64(maxMB) << 20
	var out []outgoingFile
	seen := map[string]bool{}
	var total int64
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		abs = filepath.Clean(abs)
		if seen[abs] {
			continue
		}
		seen[abs] = true
		st, err := os.Stat(abs)
		if err != nil {
			return nil, 0, fmt.Errorf("无法读取 %s：%w", filepath.Base(abs), err)
		}
		if st.IsDir() {
			return nil, 0, fmt.Errorf("暂不支持发送文件夹：%s", filepath.Base(abs))
		}
		if !st.Mode().IsRegular() {
			continue
		}
		if len(out) >= maxFilesPerTransfer {
			return nil, 0, fmt.Errorf("一次最多发送 %d 个文件", maxFilesPerTransfer)
		}
		total += st.Size()
		if total > maxTotal {
			return nil, 0, fmt.Errorf("文件总大小超过 %d MB 上限", maxMB)
		}
		out = append(out, outgoingFile{Path: abs, Name: filepath.Base(abs), Size: st.Size()})
	}
	if len(out) == 0 {
		return nil, 0, errors.New("没有可发送的普通文件")
	}
	return out, total, nil
}

// BroadcastFiles sends one file set to every connected peer in parallel.
func (h *hub) BroadcastFiles(files []outgoingFile, total int64) int {
	h.mu.Lock()
	conns := make([]*conn, 0, len(h.conns))
	for c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.Unlock()
	n := 0
	for _, c := range conns {
		cc := c
		if h.goTrack(func() { h.app.sendFilesTo(cc, files, total) }) {
			n++
		}
	}
	return n
}

func (a *App) sendFilesTo(c *conn, files []outgoingFile, total int64) {
	c.sendFileMu.Lock()
	defer c.sendFileMu.Unlock()
	if c.ctx.Err() != nil {
		return
	}
	a.transfer.start("out", c.peerName, len(files), total)
	fail := func(err error) {
		a.transfer.finish(err.Error())
		a.events.Add("sys", "error", fmt.Sprintf("向 %s 发送文件失败：%v", c.peerName, err))
		c.close("文件发送失败")
	}

	manifest := proto.FilesManifest{Files: make([]proto.FileMeta, len(files)), Total: total}
	for i, f := range files {
		manifest.Files[i] = proto.FileMeta{Name: f.Name, Size: f.Size}
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		fail(err)
		return
	}
	if err := c.sendBlocking(proto.TypeFiles, data); err != nil {
		fail(err)
		return
	}
	buf := make([]byte, proto.MaxChunk)
	for i, f := range files {
		file, err := os.Open(f.Path)
		if err != nil {
			fail(err)
			return
		}
		remaining := f.Size
		for remaining > 0 {
			n := int64(len(buf))
			if remaining < n {
				n = remaining
			}
			if _, err := io.ReadFull(file, buf[:n]); err != nil {
				file.Close()
				fail(fmt.Errorf("%s：%w", f.Name, err))
				return
			}
			if err := c.sendBlocking(proto.TypeFileChunk, proto.EncodeChunk(uint32(i), buf[:n])); err != nil {
				file.Close()
				fail(err)
				return
			}
			remaining -= n
			a.transfer.add(n)
			a.stats.bytesOut.Add(n)
		}
		file.Close()
	}
	if err := c.sendBlocking(proto.TypeFilesDone, nil); err != nil {
		fail(err)
		return
	}
	a.transfer.finish("")
	a.events.Add("out", "files", fmt.Sprintf("已向 %s 发送 %d 个文件（%s）", c.peerName, len(files), humanBytes(total)))
}

type fileReceiver struct {
	app      *App
	peer     string
	dir      string
	metas    []proto.FileMeta
	paths    []string
	files    []*os.File
	received []int64
	total    int64
	done     int64
	discard  bool
}

func (c *conn) beginTransfer(manifest proto.FilesManifest) error {
	c.fileMu.Lock()
	defer c.fileMu.Unlock()
	if c.recv != nil {
		return errors.New("已有文件传输正在进行")
	}
	r, err := newFileReceiver(c.app, c.peerName, manifest)
	if err != nil {
		return err
	}
	c.recv = r
	if !r.discard {
		c.app.transfer.start("in", c.peerName, len(r.metas), r.total)
		c.app.events.Add("in", "files", fmt.Sprintf("正在接收来自 %s 的 %d 个文件（%s）", c.peerName, len(r.metas), humanBytes(r.total)))
	}
	return nil
}

func newFileReceiver(a *App, peer string, m proto.FilesManifest) (*fileReceiver, error) {
	if len(m.Files) == 0 || len(m.Files) > maxFilesPerTransfer {
		return nil, fmt.Errorf("文件数量无效（%d）", len(m.Files))
	}
	cfg := a.Config()
	maxTotal := int64(cfg.MaxFileMB) << 20
	var sum int64
	for _, f := range m.Files {
		if f.Size < 0 {
			return nil, errors.New("文件大小无效")
		}
		sum += f.Size
		if sum < 0 || sum > maxTotal {
			return nil, fmt.Errorf("文件总大小超过 %d MB 上限", cfg.MaxFileMB)
		}
	}
	if sum != m.Total {
		return nil, errors.New("文件清单大小不一致")
	}
	r := &fileReceiver{
		app:      a,
		peer:     peer,
		metas:    m.Files,
		files:    make([]*os.File, len(m.Files)),
		received: make([]int64, len(m.Files)),
		total:    m.Total,
		discard:  cfg.Paused || !cfg.SyncFiles,
	}
	if r.discard {
		return r, nil
	}
	dir, err := makeReceiveDir(a.downloadRoot)
	if err != nil {
		return nil, err
	}
	r.dir = dir
	used := map[string]bool{}
	for _, f := range m.Files {
		name := uniqueFileName(safeFileName(f.Name), used)
		used[strings.ToLower(name)] = true
		r.paths = append(r.paths, filepath.Join(dir, name))
	}
	return r, nil
}

func makeReceiveDir(root string) (string, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	stamp := time.Now().Format("2006-01-02_15-04-05")
	for i := 0; i < 1000; i++ {
		name := stamp
		if i > 0 {
			name = fmt.Sprintf("%s-%d", stamp, i)
		}
		dir := filepath.Join(root, name)
		err := os.Mkdir(dir, 0o755)
		if err == nil {
			return dir, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", errors.New("无法创建接收目录")
}

func (c *conn) writeTransferChunk(payload []byte) error {
	index, data, err := proto.ChunkIndex(payload)
	if err != nil {
		return err
	}
	if len(data) > proto.MaxChunk {
		return errors.New("文件分块过大")
	}
	c.fileMu.Lock()
	defer c.fileMu.Unlock()
	r := c.recv
	if r == nil {
		return errors.New("没有进行中的文件传输")
	}
	i := int(index)
	if i < 0 || i >= len(r.metas) {
		return errors.New("文件索引无效")
	}
	if r.received[i]+int64(len(data)) > r.metas[i].Size {
		return errors.New("文件分块超过声明大小")
	}
	if !r.discard {
		if r.files[i] == nil {
			f, err := os.OpenFile(r.paths[i], os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			r.files[i] = f
		}
		if _, err := r.files[i].Write(data); err != nil {
			return err
		}
		r.app.stats.bytesIn.Add(int64(len(data)))
	}
	r.received[i] += int64(len(data))
	r.done += int64(len(data))
	if !r.discard {
		r.app.transfer.add(int64(len(data)))
	}
	return nil
}

func (c *conn) finishTransfer() ([]string, error) {
	c.fileMu.Lock()
	defer c.fileMu.Unlock()
	r := c.recv
	if r == nil {
		return nil, errors.New("没有进行中的文件传输")
	}
	c.recv = nil
	if r.discard {
		c.app.transfer.finish("")
		return nil, nil
	}
	ok := true
	for i := range r.metas {
		if r.received[i] != r.metas[i].Size {
			ok = false
			break
		}
	}
	if !ok {
		r.abort()
		c.app.transfer.finish("文件大小校验失败")
		return nil, errors.New("文件大小校验失败")
	}
	for i := range r.metas {
		if r.files[i] == nil {
			f, err := os.OpenFile(r.paths[i], os.O_CREATE|os.O_WRONLY, 0o644)
			if err != nil {
				r.abort()
				c.app.transfer.finish(err.Error())
				return nil, err
			}
			r.files[i] = f
		}
	}
	r.abortFilesOnly()
	cfg := c.app.Config()
	if cfg.Paused || !cfg.SyncFiles {
		_ = os.RemoveAll(r.dir)
		c.app.transfer.finish("")
		return nil, nil
	}
	c.app.transfer.finish("")
	return r.paths, nil
}

func (c *conn) abortTransfer() {
	c.fileMu.Lock()
	r := c.recv
	c.recv = nil
	c.fileMu.Unlock()
	if r != nil {
		r.abort()
		c.app.transfer.finish("传输中断")
	}
}

func (r *fileReceiver) abort() {
	r.abortFilesOnly()
	if r.dir != "" {
		_ = os.RemoveAll(r.dir)
	}
}

func (r *fileReceiver) abortFilesOnly() {
	for _, f := range r.files {
		if f != nil {
			_ = f.Close()
		}
	}
}

var reservedWindowsNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// safeFileName strips path components and characters that are illegal or
// dangerous on either Linux or Windows.
func safeFileName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(strings.TrimSpace(name))
	name = strings.TrimRight(name, ". ")
	if name == "" || name == "." || name == ".." || name == "/" {
		name = "file"
	}
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || strings.ContainsRune(`<>:"/\\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		name = "file"
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	if ext == "." {
		ext, base = "", name
	}
	base = strings.TrimRight(base, ". ")
	if base == "" {
		base = "file"
	}
	if reservedWindowsNames[strings.ToLower(strings.TrimSpace(base))] {
		base = "_" + base
	}
	runes := []rune(base)
	if len(runes) > 100 {
		base = string(runes[:100])
	}
	extRunes := []rune(ext)
	if len(extRunes) > 20 {
		ext = string(extRunes[:20])
	}
	// Filesystem name limits are in bytes, not runes. Leave room for the
	// receive directory and a " (123)" collision suffix.
	base = truncateUTF8(base, 140)
	ext = truncateUTF8(ext, 32)
	if !utf8.ValidString(base) || !utf8.ValidString(ext) {
		return "file"
	}
	return base + ext
}

func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for len(s) > max {
		_, size := utf8.DecodeLastRuneInString(s)
		if size <= 0 {
			break
		}
		s = s[:len(s)-size]
	}
	return s
}

func uniqueFileName(name string, used map[string]bool) string {
	if !used[strings.ToLower(name)] {
		return name
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; i < 10000; i++ {
		cand := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if !used[strings.ToLower(cand)] {
			return cand
		}
	}
	return fmt.Sprintf("file-%d", time.Now().UnixNano())
}
