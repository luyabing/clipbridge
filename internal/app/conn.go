package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"clipbridge/internal/config"
	"clipbridge/internal/proto"
)

type outMessage struct {
	typ     byte
	payload []byte
}

type conn struct {
	app *App
	hub *hub
	raw net.Conn

	ctx    context.Context
	cancel context.CancelFunc

	codec      *proto.Codec
	peerID     string
	peerName   string
	peerOS     string
	secure     bool
	primary    bool
	outbound   bool
	remoteAddr string
	connected  time.Time

	sendCh    chan outMessage
	closeOnce sync.Once

	sendFileMu sync.Mutex
	fileMu     sync.Mutex
	recv       *fileReceiver
}

type handshakeResult struct {
	peerID   string
	peerName string
	peerOS   string
	secure   bool
	key      []byte
	addr     string
}

// handleConn performs the handshake and, if accepted, starts the read/write
// loops. It is called for both inbound and outbound TCP connections.
func (a *App) handleConn(raw net.Conn, outbound bool, dialAddr string) {
	_ = raw.SetDeadline(time.Now().Add(10 * time.Second))
	if tc, ok := raw.(*net.TCPConn); ok {
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(30 * time.Second)
	}
	cfg := a.Config()
	res, err := a.handshake(raw, cfg, outbound)
	if err != nil {
		a.debugf("来自 %s 的握手失败: %v", raw.RemoteAddr(), err)
		addr := res.addr
		if addr == "" {
			addr = dialAddr
		}
		if addr == "" {
			addr = raw.RemoteAddr().String()
		}
		a.hub.PeerError(res.peerID, res.peerName, addr, err.Error())
		raw.Close()
		return
	}
	_ = raw.SetDeadline(time.Time{})

	var codec *proto.Codec
	if res.secure {
		codec, err = proto.SecureCodec(res.key, proto.MaxFrame)
		if err != nil {
			a.logf("初始化加密通道失败: %v", err)
			raw.Close()
			return
		}
	} else {
		codec = proto.PlainCodec(proto.MaxFrame)
	}

	ctx, cancel := context.WithCancel(a.hub.ctx)
	// Both sides may dial at the same time. Keep the connection initiated by
	// the device with the smaller ID, and let both ends agree on which one
	// that is.
	initiatorID, responderID := cfg.DeviceID, res.peerID
	if !outbound {
		initiatorID, responderID = res.peerID, cfg.DeviceID
	}
	c := &conn{
		app:        a,
		hub:        a.hub,
		raw:        raw,
		ctx:        ctx,
		cancel:     cancel,
		codec:      codec,
		peerID:     res.peerID,
		peerName:   res.peerName,
		peerOS:     res.peerOS,
		secure:     res.secure,
		primary:    res.peerID != "" && initiatorID < responderID,
		outbound:   outbound,
		remoteAddr: res.addr,
		connected:  time.Now(),
		sendCh:     make(chan outMessage, 8),
	}
	if !a.hub.addConn(c) {
		return
	}
	a.hub.goTrack(c.readLoop)
	a.hub.goTrack(c.writeLoop)
}

func (a *App) handshake(raw net.Conn, cfg config.Config, outbound bool) (res handshakeResult, err error) {
	codec := proto.PlainCodec(proto.HandshakeFrameLimit)
	localNonce, err := proto.NewNonce()
	if err != nil {
		return res, err
	}
	localHello := proto.Hello{
		Magic:   proto.Magic,
		Version: proto.Version,
		ID:      cfg.DeviceID,
		Name:    cfg.DeviceName,
		OS:      a.osName(),
		Port:    cfg.TCPPort,
		Secure:  cfg.PairCode != "",
		Nonce:   localNonce,
	}
	localSecure := cfg.PairCode != ""

	if outbound {
		f, _ := proto.HelloFrame(localHello)
		if err := codec.Write(raw, f.Type, f.Payload); err != nil {
			return res, err
		}
		pf, err := codec.Read(raw)
		if err != nil {
			return res, err
		}
		if pf.Type == proto.TypeError {
			return res, errors.New(proto.ParseError(pf))
		}
		peerHello, err := proto.ParseHello(pf)
		if err != nil {
			return res, err
		}
		res.peerID, res.peerName, res.peerOS = peerHello.ID, peerHello.Name, peerHello.OS
		res.addr = net.JoinHostPort(hostOnly(raw.RemoteAddr()), strconv.Itoa(peerHello.Port))
		if peerHello.ID == cfg.DeviceID {
			return res, errors.New("检测到重复的设备 ID")
		}
		if peerHello.Secure != localSecure {
			msg := "配对码设置不一致：两端必须同时设置或同时留空"
			sendPlainError(raw, codec, msg)
			return res, errors.New(msg)
		}
		if localSecure {
			key := proto.AuthKey(cfg.PairCode)
			got, derr := base64.RawURLEncoding.DecodeString(peerHello.Proof)
			want := proto.Proof(key, "server", cfg.DeviceID, peerHello.ID, localNonce, peerHello.Nonce)
			if derr != nil || !proto.VerifyProof(want, got) {
				sendPlainError(raw, codec, "配对码不一致")
				time.Sleep(350 * time.Millisecond)
				return res, errors.New("配对码不一致")
			}
			proof := proto.Proof(key, "client", cfg.DeviceID, peerHello.ID, localNonce, peerHello.Nonce)
			af := proto.AuthFrame(proof)
			if err := codec.Write(raw, af.Type, af.Payload); err != nil {
				return res, err
			}
		} else {
			af := proto.AuthFrame(nil)
			if err := codec.Write(raw, af.Type, af.Payload); err != nil {
				return res, err
			}
		}
		rf, err := codec.Read(raw)
		if err != nil {
			return res, err
		}
		if rf.Type == proto.TypeError {
			return res, errors.New(proto.ParseError(rf))
		}
		if rf.Type != proto.TypeReady {
			return res, fmt.Errorf("握手失败（类型 %d）", rf.Type)
		}
		res.secure = localSecure
		if localSecure {
			res.key = proto.EncKey(cfg.PairCode, cfg.DeviceID, peerHello.ID, localNonce, peerHello.Nonce)
		}
		return res, nil
	}

	// Responder.
	pf, err := codec.Read(raw)
	if err != nil {
		return res, err
	}
	if pf.Type == proto.TypeError {
		return res, errors.New(proto.ParseError(pf))
	}
	peerHello, err := proto.ParseHello(pf)
	if err != nil {
		sendPlainError(raw, codec, "协议握手无效")
		return res, err
	}
	res.peerID, res.peerName, res.peerOS = peerHello.ID, peerHello.Name, peerHello.OS
	res.addr = net.JoinHostPort(hostOnly(raw.RemoteAddr()), strconv.Itoa(peerHello.Port))
	if peerHello.ID == cfg.DeviceID {
		sendPlainError(raw, codec, "检测到重复的设备 ID")
		return res, errors.New("检测到重复的设备 ID")
	}
	if peerHello.Secure != localSecure {
		msg := "配对码设置不一致：两端必须同时设置或同时留空"
		sendPlainError(raw, codec, msg)
		return res, errors.New(msg)
	}

	key := []byte(nil)
	local := localHello
	if localSecure {
		key = proto.AuthKey(cfg.PairCode)
		local.Proof = base64.RawURLEncoding.EncodeToString(
			proto.Proof(key, "server", peerHello.ID, cfg.DeviceID, peerHello.Nonce, localNonce))
	}
	f, _ := proto.HelloFrame(local)
	if err := codec.Write(raw, f.Type, f.Payload); err != nil {
		return res, err
	}
	af, err := codec.Read(raw)
	if err != nil {
		return res, err
	}
	if af.Type == proto.TypeError {
		return res, errors.New(proto.ParseError(af))
	}
	got, err := proto.ParseAuth(af)
	if err != nil {
		sendPlainError(raw, codec, "认证数据无效")
		return res, err
	}
	if localSecure {
		want := proto.Proof(key, "client", peerHello.ID, cfg.DeviceID, peerHello.Nonce, localNonce)
		if !proto.VerifyProof(want, got) {
			sendPlainError(raw, codec, "配对码不一致")
			time.Sleep(350 * time.Millisecond)
			return res, errors.New("配对码不一致")
		}
	}
	if err := codec.Write(raw, proto.TypeReady, nil); err != nil {
		return res, err
	}
	res.secure = localSecure
	if localSecure {
		res.key = proto.EncKey(cfg.PairCode, peerHello.ID, cfg.DeviceID, peerHello.Nonce, localNonce)
	}
	return res, nil
}

func sendPlainError(w net.Conn, codec *proto.Codec, msg string) {
	f := proto.ErrorFrame(msg)
	_ = w.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_ = codec.Write(w, f.Type, f.Payload)
}

func hostOnly(addr net.Addr) string {
	host, _, err := net.SplitHostPort(addr.String())
	if err == nil {
		return host
	}
	return addr.String()
}

func (c *conn) direction() string {
	if c.outbound {
		return "out"
	}
	return "in"
}

// TrySend queues a message without blocking. Clipboard bursts are allowed to
// drop a frame if a slow peer is backed up; the next copy will be sent.
func (c *conn) TrySend(typ byte, payload []byte) bool {
	select {
	case <-c.ctx.Done():
		return false
	default:
	}
	cp := append([]byte(nil), payload...)
	select {
	case c.sendCh <- outMessage{typ: typ, payload: cp}:
		return true
	case <-c.ctx.Done():
		return false
	default:
		c.app.debugf("发送队列已满，跳过一帧（%s）", c.peerName)
		return false
	}
}

func (c *conn) sendBlocking(typ byte, payload []byte) error {
	cp := append([]byte(nil), payload...)
	select {
	case c.sendCh <- outMessage{typ: typ, payload: cp}:
		return nil
	case <-c.ctx.Done():
		return context.Canceled
	}
}

func (c *conn) writeLoop() {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case m := <-c.sendCh:
			_ = c.raw.SetWriteDeadline(time.Now().Add(60 * time.Second))
			if err := c.codec.Write(c.raw, m.typ, m.payload); err != nil {
				c.close("写入失败: " + err.Error())
				return
			}
		case <-ticker.C:
			_ = c.raw.SetWriteDeadline(time.Now().Add(30 * time.Second))
			if err := c.codec.Write(c.raw, proto.TypePing, nil); err != nil {
				c.close("心跳失败")
				return
			}
		}
	}
}

func (c *conn) readLoop() {
	defer c.close("连接结束")
	for {
		_ = c.raw.SetReadDeadline(time.Now().Add(75 * time.Second))
		f, err := c.codec.Read(c.raw)
		if err != nil {
			c.app.debugf("从 %s 读取失败: %v", c.peerName, err)
			return
		}
		c.hub.Touch(c.peerID)
		switch f.Type {
		case proto.TypePing:
			c.TrySend(proto.TypePong, nil)
		case proto.TypePong:
		case proto.TypeText:
			if err := ValidateText(f.Payload); err != nil {
				c.app.events.Add("sys", "error", "丢弃来自 "+c.peerName+" 的文本："+err.Error())
				continue
			}
			c.app.acceptRemote(c.peerName, ClipText, f.Payload, nil)
		case proto.TypeImage:
			if err := ValidatePNG(f.Payload); err != nil {
				c.app.events.Add("sys", "error", "丢弃来自 "+c.peerName+" 的图片："+err.Error())
				continue
			}
			c.app.acceptRemote(c.peerName, ClipImage, f.Payload, nil)
		case proto.TypeFiles:
			if len(f.Payload) > proto.HandshakeFrameLimit {
				c.close("文件清单过大")
				return
			}
			var manifest proto.FilesManifest
			if err := json.Unmarshal(f.Payload, &manifest); err != nil {
				c.close("文件清单格式错误")
				return
			}
			if err := c.beginTransfer(manifest); err != nil {
				c.app.events.Add("sys", "error", "接收文件失败："+err.Error())
				c.close("文件传输失败")
				return
			}
		case proto.TypeFileChunk:
			if err := c.writeTransferChunk(f.Payload); err != nil {
				c.app.events.Add("sys", "error", "接收文件失败："+err.Error())
				c.close("文件传输失败")
				return
			}
		case proto.TypeFilesDone:
			paths, err := c.finishTransfer()
			if err != nil {
				c.app.events.Add("sys", "error", "接收文件失败："+err.Error())
				c.close("文件传输失败")
				return
			}
			if len(paths) > 0 {
				c.app.acceptRemote(c.peerName, ClipFiles, nil, paths)
			}
		case proto.TypeError:
			c.close("对方报告：" + proto.ParseError(f))
			return
		default:
			c.close(fmt.Sprintf("协议帧类型无效（%d）", f.Type))
			return
		}
	}
}

func (c *conn) close(reason string) {
	c.closeOnce.Do(func() {
		c.cancel()
		_ = c.raw.Close()
		c.abortTransfer()
		c.hub.removeConn(c)
		c.app.debugf("连接 %s 已关闭：%s", c.peerName, reason)
	})
}

// acceptRemote applies a clipboard update received from peerName.
func (a *App) acceptRemote(peerName string, kind ClipKind, data []byte, files []string) {
	cfg := a.Config()
	if cfg.Paused {
		a.events.Add("sys", "config", "已暂停，忽略来自 "+peerName+" 的剪贴板")
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
		a.debugf("忽略禁用类型的远程剪贴板 %s", kind)
		return
	}
	switch kind {
	case ClipText:
		if err := ValidateText(data); err != nil {
			a.events.Add("sys", "error", "拒绝远程文本："+err.Error())
			return
		}
	case ClipImage:
		if err := ValidatePNG(data); err != nil {
			a.events.Add("sys", "error", "拒绝远程图片："+err.Error())
			return
		}
	case ClipFiles:
		if len(files) == 0 {
			return
		}
		a.rememberTransferredFiles(files)
	}
	a.markRemote(kind, data, files)
	ctx, cancel := context.WithTimeout(a.ctx, 8*time.Second)
	defer cancel()
	if err := a.clip.Write(ctx, kind, data, files); err != nil {
		a.events.Add("sys", "error", "写入系统剪贴板失败："+err.Error())
		return
	}
	switch kind {
	case ClipText:
		a.stats.received.Add(1)
		a.stats.bytesIn.Add(int64(len(data)))
		a.events.Add("in", "text", fmt.Sprintf("从 %s 收到文本（%d 字）", peerName, a.charCount(data)))
	case ClipImage:
		a.stats.received.Add(1)
		a.stats.bytesIn.Add(int64(len(data)))
		a.events.Add("in", "image", fmt.Sprintf("从 %s 收到图片（%s）", peerName, humanBytes(int64(len(data)))))
	case ClipFiles:
		a.stats.received.Add(1)
		a.events.Add("in", "files", fmt.Sprintf("从 %s 收到 %d 个文件", peerName, len(files)))
	}
}
