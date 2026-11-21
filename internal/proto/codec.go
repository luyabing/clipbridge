// Package proto implements the framed wire protocol used between ClipBridge
// peers. Every frame is length-prefixed, so clipboard payloads (including PNG
// images and file chunks) can be streamed without base64 inflation.
package proto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	// Magic identifies the application on the wire.
	Magic = "ClipBridge"
	// Version is the protocol version.
	Version = 1

	// TypeHello starts a handshake.
	TypeHello byte = 1
	// TypeAuth carries the initiator's challenge response.
	TypeAuth byte = 2
	// TypeReady confirms a completed handshake.
	TypeReady byte = 3
	// TypeError carries a human readable handshake failure.
	TypeError byte = 4
	// TypeText carries UTF-8 clipboard text.
	TypeText byte = 5
	// TypeImage carries a PNG image.
	TypeImage byte = 6
	// TypeFiles carries a JSON file manifest.
	TypeFiles byte = 7
	// TypeFileChunk carries a 4-byte file index followed by raw bytes.
	TypeFileChunk byte = 8
	// TypeFilesDone marks a completed file transfer.
	TypeFilesDone byte = 9
	// TypePing keeps an idle connection alive.
	TypePing byte = 10
	// TypePong answers a ping.
	TypePong byte = 11
)

const (
	// MaxFrame bounds an individual frame. File transfers use small chunks,
	// so this only needs to fit one clipboard image (64 MiB cap).
	MaxFrame = 80 << 20
	// HandshakeFrameLimit bounds handshake JSON frames.
	HandshakeFrameLimit = 1 << 20
	// MaxChunk is the raw file payload size per frame.
	MaxChunk = 256 << 10
)

// Frame is one decoded protocol message.
type Frame struct {
	Type    byte
	Payload []byte
}

// Codec reads and writes framed messages. A nil AEAD means plaintext, which is
// only used during the handshake and when no pairing code is configured.
type Codec struct {
	aead     cipher.AEAD
	maxFrame int
}

// PlainCodec returns a codec without transport encryption.
func PlainCodec(maxFrame int) *Codec {
	if maxFrame <= 0 {
		maxFrame = MaxFrame
	}
	return &Codec{maxFrame: maxFrame}
}

// SecureCodec returns a codec that encrypts every frame with AES-256-GCM.
// key must be 32 bytes.
func SecureCodec(key []byte, maxFrame int) (*Codec, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("proto: encryption key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if maxFrame <= 0 {
		maxFrame = MaxFrame
	}
	return &Codec{aead: aead, maxFrame: maxFrame}, nil
}

// Write sends one frame.
func (c *Codec) Write(w io.Writer, typ byte, payload []byte) error {
	if c.aead == nil {
		if 1+len(payload) > c.maxFrame {
			return fmt.Errorf("proto: frame too large (%d bytes)", 1+len(payload))
		}
		buf := make([]byte, 4+1+len(payload))
		binary.BigEndian.PutUint32(buf[:4], uint32(1+len(payload)))
		buf[4] = typ
		copy(buf[5:], payload)
		return writeFull(w, buf)
	}

	plain := make([]byte, 1+len(payload))
	plain[0] = typ
	copy(plain[1:], payload)
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("proto: nonce: %w", err)
	}
	sealed := c.aead.Seal(nil, nonce, plain, nil)
	bodyLen := len(nonce) + len(sealed)
	if bodyLen > c.maxFrame {
		return fmt.Errorf("proto: frame too large (%d bytes)", bodyLen)
	}
	buf := make([]byte, 4+bodyLen)
	binary.BigEndian.PutUint32(buf[:4], uint32(bodyLen))
	copy(buf[4:], nonce)
	copy(buf[4+len(nonce):], sealed)
	return writeFull(w, buf)
}

// Read reads one frame. The returned payload is only valid until the next
// Read call if callers choose to reuse buffers, but this implementation
// allocates a fresh payload for each frame.
func (c *Codec) Read(r io.Reader) (Frame, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Frame{}, err
	}
	n := int(binary.BigEndian.Uint32(hdr[:]))
	if n < 1 {
		return Frame{}, errors.New("proto: empty frame")
	}
	if n > c.maxFrame {
		return Frame{}, fmt.Errorf("proto: frame exceeds limit (%d > %d)", n, c.maxFrame)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return Frame{}, err
	}
	if c.aead == nil {
		return Frame{Type: body[0], Payload: body[1:]}, nil
	}

	nonceSize := c.aead.NonceSize()
	if n < nonceSize+c.aead.Overhead()+1 {
		return Frame{}, errors.New("proto: encrypted frame too short")
	}
	plain, err := c.aead.Open(nil, body[:nonceSize], body[nonceSize:], nil)
	if err != nil {
		return Frame{}, errors.New("proto: authentication failed")
	}
	if len(plain) < 1 {
		return Frame{}, errors.New("proto: empty plaintext frame")
	}
	return Frame{Type: plain[0], Payload: plain[1:]}, nil
}

func writeFull(w io.Writer, buf []byte) error {
	for len(buf) > 0 {
		n, err := w.Write(buf)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		buf = buf[n:]
	}
	return nil
}
