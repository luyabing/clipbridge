package proto

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func validHello() Hello {
	return Hello{
		Magic:   Magic,
		Version: Version,
		ID:      "0123456789abcdef0123456789abcdef",
		Name:    "测试机",
		OS:      "Linux",
		Port:    45876,
		Nonce:   "abcdefghijklmnop",
	}
}

func TestHelloRoundTrip(t *testing.T) {
	h := validHello()
	h.Secure = true
	h.Proof = "proof"
	f, err := HelloFrame(h)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseHello(f)
	if err != nil {
		t.Fatal(err)
	}
	if got != h {
		t.Fatalf("roundtrip mismatch: %+v != %+v", got, h)
	}
}

func TestHelloValidation(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Hello)
		want string
	}{
		{"bad magic", func(h *Hello) { h.Magic = "Other" }, "magic"},
		{"bad version", func(h *Hello) { h.Version = 99 }, "版本"},
		{"empty id", func(h *Hello) { h.ID = "" }, "设备 ID"},
		{"empty name", func(h *Hello) { h.Name = "" }, "名称"},
		{"bad port", func(h *Hello) { h.Port = 0 }, "端口"},
		{"short nonce", func(h *Hello) { h.Nonce = "x" }, "随机数"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := validHello()
			tc.mut(&h)
			if err := h.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func TestAuthFrame(t *testing.T) {
	proof := bytes.Repeat([]byte{7}, 32)
	f := AuthFrame(proof)
	got, err := ParseAuth(f)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, proof) {
		t.Fatalf("proof mismatch: %x", got)
	}

	empty, err := ParseAuth(AuthFrame(nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("expected empty proof, got %x", empty)
	}
	if _, err := ParseAuth(Frame{Type: TypeHello}); err == nil {
		t.Fatal("expected wrong frame type error")
	}
}

func TestErrorFrame(t *testing.T) {
	f := ErrorFrame("配对码不一致")
	if f.Type != TypeError {
		t.Fatalf("type = %d", f.Type)
	}
	if got := ParseError(f); got != "配对码不一致" {
		t.Fatalf("message = %q", got)
	}
	if got := ParseError(Frame{Type: TypeError, Payload: []byte("not json")}); got == "" {
		t.Fatal("expected fallback message")
	}
}

func TestProofAndEncKeyAgree(t *testing.T) {
	key := AuthKey("正确的马电池订书钉")
	init := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	resp := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	in, rn := "init-nonce-1234", "resp-nonce-5678"

	server := Proof(key, "server", init, resp, in, rn)
	gotServer, err := base64.RawURLEncoding.DecodeString(base64.RawURLEncoding.EncodeToString(server))
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyProof(server, gotServer) {
		t.Fatal("server proof did not verify")
	}
	if VerifyProof(server, Proof(key, "client", init, resp, in, rn)) {
		t.Fatal("different roles produced the same proof")
	}
	if VerifyProof(server, nil) {
		t.Fatal("nil proof verified")
	}

	k1 := EncKey("code", init, resp, in, rn)
	k2 := EncKey("code", init, resp, in, rn)
	k3 := EncKey("other", init, resp, in, rn)
	if !bytes.Equal(k1, k2) {
		t.Fatal("EncKey is not deterministic")
	}
	if bytes.Equal(k1, k3) {
		t.Fatal("different pairing codes produced the same key")
	}
	if len(k1) != 32 {
		t.Fatalf("key length = %d", len(k1))
	}
}

func TestChunkHelpers(t *testing.T) {
	data := []byte("file chunk")
	payload := EncodeChunk(3, data)
	idx, got, err := ChunkIndex(payload)
	if err != nil {
		t.Fatal(err)
	}
	if idx != 3 || !bytes.Equal(got, data) {
		t.Fatalf("idx=%d data=%q", idx, got)
	}
	if _, _, err := ChunkIndex([]byte{1, 2}); err == nil {
		t.Fatal("expected short chunk error")
	}
}

func TestNewNonceUnique(t *testing.T) {
	a, err := NewNonce()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewNonce()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("nonces are identical")
	}
	if len(a) < 8 {
		t.Fatalf("nonce too short: %q", a)
	}
}
