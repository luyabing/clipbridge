package proto

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// Hello is exchanged at the start of every connection. The Proof field is set
// by the responder; the initiator proves itself with an Auth frame.
type Hello struct {
	Magic   string `json:"magic"`
	Version int    `json:"version"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	OS      string `json:"os"`
	Port    int    `json:"port"`
	Secure  bool   `json:"secure"`
	Nonce   string `json:"nonce"`
	Proof   string `json:"proof,omitempty"`
}

// Auth carries the initiator's challenge response.
type Auth struct {
	Proof string `json:"proof"`
}

// ErrorMessage is sent when the handshake cannot continue.
type ErrorMessage struct {
	Message string `json:"message"`
}

// FileMeta describes one file in a manifest.
type FileMeta struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// FilesManifest describes a set of files that follows in chunk frames.
type FilesManifest struct {
	Files []FileMeta `json:"files"`
	Total int64      `json:"total"`
}

// NewNonce returns a fresh random handshake nonce.
func NewNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Validate checks the long-lived fields of a hello message.
func (h Hello) Validate() error {
	if h.Magic != Magic {
		return fmt.Errorf("不是剪贴桥协议（magic=%q）", h.Magic)
	}
	if h.Version != Version {
		return fmt.Errorf("协议版本不兼容（对方 v%d，本机 v%d）", h.Version, Version)
	}
	if len(h.ID) < 8 || len(h.ID) > 128 {
		return errors.New("设备 ID 无效")
	}
	if h.Name == "" || len(h.Name) > 200 {
		return errors.New("设备名称无效")
	}
	if h.Port < 1 || h.Port > 65535 {
		return errors.New("端口无效")
	}
	if len(h.Nonce) < 8 || len(h.Nonce) > 128 {
		return errors.New("握手随机数无效")
	}
	return nil
}

// HelloFrame encodes a hello message as a frame.
func HelloFrame(h Hello) (Frame, error) {
	b, err := json.Marshal(h)
	if err != nil {
		return Frame{}, err
	}
	return Frame{Type: TypeHello, Payload: b}, nil
}

// ParseHello decodes a hello frame.
func ParseHello(f Frame) (Hello, error) {
	if f.Type != TypeHello {
		return Hello{}, fmt.Errorf("期望握手帧，收到类型 %d", f.Type)
	}
	var h Hello
	if err := json.Unmarshal(f.Payload, &h); err != nil {
		return Hello{}, err
	}
	if err := h.Validate(); err != nil {
		return Hello{}, err
	}
	return h, nil
}

// AuthFrame encodes an auth message.
func AuthFrame(proof []byte) Frame {
	if len(proof) == 0 {
		return Frame{Type: TypeAuth, Payload: []byte("{}")}
	}
	b, _ := json.Marshal(Auth{Proof: base64.RawURLEncoding.EncodeToString(proof)})
	return Frame{Type: TypeAuth, Payload: b}
}

// ParseAuth decodes an auth frame.
func ParseAuth(f Frame) ([]byte, error) {
	if f.Type != TypeAuth {
		return nil, fmt.Errorf("期望认证帧，收到类型 %d", f.Type)
	}
	var a Auth
	if err := json.Unmarshal(f.Payload, &a); err != nil {
		return nil, err
	}
	if a.Proof == "" {
		return nil, nil
	}
	return base64.RawURLEncoding.DecodeString(a.Proof)
}

// ErrorFrame encodes a user-visible handshake error.
func ErrorFrame(msg string) Frame {
	b, _ := json.Marshal(ErrorMessage{Message: msg})
	return Frame{Type: TypeError, Payload: b}
}

// ParseError decodes a handshake error.
func ParseError(f Frame) string {
	var e ErrorMessage
	if f.Type == TypeError && json.Unmarshal(f.Payload, &e) == nil && e.Message != "" {
		return e.Message
	}
	return "连接被对方拒绝"
}

// AuthKey derives the long-term handshake key from the pairing code.
func AuthKey(pairCode string) []byte {
	h := sha256.Sum256([]byte("ClipBridge auth key v1\x00" + pairCode))
	return h[:]
}

// EncKey derives a per-connection transport key. Binding it to both nonces
// avoids reusing an AES-GCM key/nonce pair across connections.
func EncKey(pairCode, initiatorID, responderID, initNonce, respNonce string) []byte {
	h := sha256.New()
	h.Write([]byte("ClipBridge transport key v1\x00"))
	for _, s := range []string{pairCode, initiatorID, responderID, initNonce, respNonce} {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	return h.Sum(nil)
}

// Proof computes an HMAC over the connection's identity and both nonces.
func Proof(key []byte, role, initiatorID, responderID, initNonce, respNonce string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("ClipBridge proof v1\n"))
	mac.Write([]byte(role))
	mac.Write([]byte{'\n'})
	mac.Write([]byte(initiatorID))
	mac.Write([]byte{'\n'})
	mac.Write([]byte(responderID))
	mac.Write([]byte{'\n'})
	mac.Write([]byte(initNonce))
	mac.Write([]byte{'\n'})
	mac.Write([]byte(respNonce))
	return mac.Sum(nil)
}

// VerifyProof compares two proofs in constant time.
func VerifyProof(want, got []byte) bool {
	return len(want) == len(got) && len(want) > 0 && subtle.ConstantTimeCompare(want, got) == 1
}

// ChunkIndex extracts the file index from a file chunk frame.
func ChunkIndex(payload []byte) (uint32, []byte, error) {
	if len(payload) < 4 {
		return 0, nil, errors.New("文件分块为空")
	}
	return uint32(payload[0])<<24 | uint32(payload[1])<<16 | uint32(payload[2])<<8 | uint32(payload[3]), payload[4:], nil
}

// EncodeChunk prefixes raw data with a file index.
func EncodeChunk(index uint32, data []byte) []byte {
	out := make([]byte, 4+len(data))
	out[0] = byte(index >> 24)
	out[1] = byte(index >> 16)
	out[2] = byte(index >> 8)
	out[3] = byte(index)
	copy(out[4:], data)
	return out
}
