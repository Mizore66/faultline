package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReadStandardInputLimit(t *testing.T) {
	e := &env{stdin: strings.NewReader(strings.Repeat("\xff", 1_333_334))}
	if _, err := e.readStandardInput(); err == nil || err.Error() != "Lifecycle stdin exceeds the 4 MB safety limit." {
		t.Fatalf("%v", err)
	}
	// 1,333,333 U+FFFD are 3,999,999 UTF-8 bytes: under the limit.
	e = &env{stdin: strings.NewReader(strings.Repeat("\xff", 1_333_333))}
	if s, err := e.readStandardInput(); err != nil || len(s) != 3_999_999 {
		t.Fatalf("%d %v", len(s), err)
	}
	e = &env{stdin: bytes.NewReader([]byte("a\xe2\x82"))}
	if s, err := e.readStandardInput(); err != nil || s != "a�" {
		t.Fatalf("%q %v", s, err)
	}
}

// TestReadStandardInputFiles covers the stdin kinds Node's getStdin
// distinguishes (lib/internal/bootstrap/switches/is_main_thread.js).
func TestReadStandardInputFiles(t *testing.T) {
	dir := t.TempDir()
	// A directory is no TTY, FILE or PIPE: Node gives a dummy empty stream.
	d, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if s, err := (&env{stdin: d}).readStandardInput(); err != nil || s != "" {
		t.Fatalf("directory: %q %v", s, err)
	}
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, []byte("x\xf0\x9f\x98"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if s, err := (&env{stdin: r}).readStandardInput(); err != nil || s != "x�" {
		t.Fatalf("file: %q %v", s, err)
	}
	if runtime.GOOS == "windows" {
		return // a directory cannot be stdin there; read errors on write-only handles differ
	}
	// A FILE opened write-only fails fs.ReadStream's read.
	w, err := os.OpenFile(file, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := (&env{stdin: w}).readStandardInput(); err == nil || err.Error() != "EBADF: bad file descriptor, read" {
		t.Fatalf("write-only file: %v", err)
	}
	// The write end of a pipe is a net.Socket that is not readable.
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	if _, err := (&env{stdin: pw}).readStandardInput(); err == nil || err.Error() != "read ENOTCONN" {
		t.Fatalf("write end of a pipe: %v", err)
	}
	go func() { pw.Write([]byte("a\r\nb")); pw.Close() }()
	if s, err := (&env{stdin: pr}).readStandardInput(); err != nil || s != "a\r\nb" {
		t.Fatalf("pipe: %q %v", s, err)
	}
}
