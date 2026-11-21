package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestSafeFileName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"../../etc/passwd", "passwd"},
		{`C:\Users\me\报告.txt`, "报告.txt"},
		{"CON", "_CON"},
		{"con.txt", "_con.txt"},
		{"LPT9.log", "_LPT9.log"},
		{`a<b>c:d"e|f?g*h.txt`, "a_b_c_d_e_f_g_h.txt"},
		{"bad\nname.txt", "bad_name.txt"},
		{"trailing. . ", "trailing"},
		{"", "file"},
		{"..", "file"},
		{"/", "file"},
		{"    ", "file"},
		{"正常文件名 2026.pdf", "正常文件名 2026.pdf"},
		{".bashrc", "file.bashrc"},
	}
	for _, tc := range cases {
		if got := safeFileName(tc.in); got != tc.want {
			t.Errorf("safeFileName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSafeFileNameTruncatesLongParts(t *testing.T) {
	cases := []string{
		strings.Repeat("长", 150) + "." + strings.Repeat("x", 30),
		strings.Repeat("😀", 120) + ".tar.gz",
	}
	for _, long := range cases {
		got := safeFileName(long)
		if len(got) > 175 {
			t.Fatalf("name is too long: %d bytes", len(got))
		}
		if !utf8.ValidString(got) {
			t.Fatalf("invalid UTF-8: %q", got)
		}
		if filepath.Ext(got) == "" {
			t.Fatalf("extension was lost: %q", got)
		}
	}
}

func TestUniqueFileName(t *testing.T) {
	used := map[string]bool{}
	first := uniqueFileName("照片.png", used)
	if first != "照片.png" {
		t.Fatalf("first = %q", first)
	}
	used[strings.ToLower(first)] = true
	second := uniqueFileName("照片.png", used)
	if second != "照片 (1).png" {
		t.Fatalf("second = %q", second)
	}
	used[strings.ToLower(second)] = true
	third := uniqueFileName("照片.png", used)
	if third != "照片 (2).png" {
		t.Fatalf("third = %q", third)
	}
}

func TestUniqueFileNameCaseInsensitive(t *testing.T) {
	used := map[string]bool{"report.txt": true}
	if got := uniqueFileName("Report.TXT", used); got != "Report (1).TXT" {
		t.Fatalf("got %q", got)
	}
}

func TestFileSetHashIgnoresPaths(t *testing.T) {
	dir1, dir2 := t.TempDir(), t.TempDir()
	write := func(dir, name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	a := write(dir1, "报告.pdf", "same content")
	b := write(dir2, "报告.pdf", "same content")
	h1, ok := fileSetHash([]string{a})
	if !ok {
		t.Fatal("first hash failed")
	}
	h2, ok := fileSetHash([]string{b})
	if !ok {
		t.Fatal("second hash failed")
	}
	if h1 != h2 {
		t.Fatal("same name and size produced different fingerprints")
	}
	c := write(dir2, "报告.pdf", "different length")
	h3, _ := fileSetHash([]string{c})
	if h1 == h3 {
		t.Fatal("different size produced the same fingerprint")
	}
	if _, ok := fileSetHash([]string{filepath.Join(dir1, "missing")}); ok {
		t.Fatal("missing file produced a fingerprint")
	}
	if _, ok := fileSetHash([]string{dir1}); ok {
		t.Fatal("directory produced a fingerprint")
	}
}

func TestRememberTransferredFiles(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &App{}
	if a.filesRecentlyTransferred([]string{p}) {
		t.Fatal("file was considered transferred before remembering")
	}
	a.rememberTransferredFiles([]string{p})
	if !a.filesRecentlyTransferred([]string{p}) {
		t.Fatal("remembered file set was not recognized")
	}
	a.fileEchoMu.Lock()
	for k := range a.fileEcho {
		a.fileEcho[k] = time.Now().Add(-time.Second)
	}
	a.fileEchoMu.Unlock()
	if a.filesRecentlyTransferred([]string{p}) {
		t.Fatal("expired file set was still recognized")
	}
}

func TestNormalizePeerAddr(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"192.168.122.5", "192.168.122.5:45876", true},
		{" 192.168.122.5 ", "192.168.122.5:45876", true},
		{"192.168.122.5:1234", "192.168.122.5:1234", true},
		{"myhost", "myhost:45876", true},
		{"[fe80::1]:1234", "[fe80::1]:1234", true},
		{"[fe80::1]", "[fe80::1]:45876", true},
		{"fe80::1", "[fe80::1]:45876", true},
		{"", "", false},
		{"   ", "", false},
		{"a b", "", false},
		{"host:abc", "", false},
		{"host:0", "", false},
		{"host:70000", "", false},
		{":1234", "", false},
	}
	for _, tc := range cases {
		got, err := normalizePeerAddr(tc.in, 45876)
		if tc.ok {
			if err != nil {
				t.Errorf("normalizePeerAddr(%q) error: %v", tc.in, err)
				continue
			}
			if got != tc.want {
				t.Errorf("normalizePeerAddr(%q) = %q, want %q", tc.in, got, tc.want)
			}
		} else if err == nil {
			t.Errorf("normalizePeerAddr(%q) = %q, want error", tc.in, got)
		}
	}
}
