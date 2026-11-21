package proto

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestPlainCodecRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	c := PlainCodec(1024)
	payload := []byte("剪贴板文本 hello")
	if err := c.Write(&buf, TypeText, payload); err != nil {
		t.Fatalf("Write: %v", err)
	}
	f, err := c.Read(&buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if f.Type != TypeText || !bytes.Equal(f.Payload, payload) {
		t.Fatalf("got type=%d payload=%q", f.Type, f.Payload)
	}
	if buf.Len() != 0 {
		t.Fatalf("unexpected trailing bytes: %d", buf.Len())
	}
}

func TestPlainCodecRejectsOversizeFrame(t *testing.T) {
	var buf bytes.Buffer
	c := PlainCodec(8)
	if err := c.Write(&buf, TypeText, bytes.Repeat([]byte("x"), 20)); err == nil {
		t.Fatal("expected write error")
	}
}

func TestSecureCodecRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	c, err := SecureCodec(key, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("加密内容"), 100)
	if err := c.Write(&buf, TypeImage, payload); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if bytes.Contains(buf.Bytes(), []byte("加密内容")) {
		t.Fatal("payload appears in ciphertext")
	}
	f, err := c.Read(&buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if f.Type != TypeImage || !bytes.Equal(f.Payload, payload) {
		t.Fatalf("roundtrip mismatch: type=%d len=%d", f.Type, len(f.Payload))
	}
}

func TestSecureCodecRejectsTamper(t *testing.T) {
	key := make([]byte, 32)
	c, err := SecureCodec(key, 1024)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := c.Write(&buf, TypeText, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	raw[len(raw)-1] ^= 0xff
	if _, err := c.Read(bytes.NewReader(raw)); err == nil {
		t.Fatal("expected authentication error")
	}
}

func TestSecureCodecRejectsBadKey(t *testing.T) {
	if _, err := SecureCodec(make([]byte, 16), 1024); err == nil {
		t.Fatal("expected key length error")
	}
}

func TestCodecRejectsZeroLength(t *testing.T) {
	c := PlainCodec(1024)
	if _, err := c.Read(bytes.NewReader([]byte{0, 0, 0, 0})); err == nil {
		t.Fatal("expected error for empty frame")
	}
}
