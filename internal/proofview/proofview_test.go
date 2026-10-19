package proofview

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRootForTest is the module root (two levels above this package).
func repoRootForTest(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestPreviewOfCommittedSample(t *testing.T) {
	repo := repoRootForTest(t)
	sample := filepath.Join(repo, "docs", "samples", "self-incident-commit-proof")
	out := filepath.Join(t.TempDir(), "preview.html")
	p, err := WriteCommitProofPreview(sample, nil, out)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := os.ReadFile(out)
	if len(page) != p.Bytes || !strings.Contains(string(page), `<meta name="faultline-preview" content="static-commit-proof-snapshot" />`) {
		t.Fatalf("bytes %d vs %d", len(page), p.Bytes)
	}
	if p.RootDigest != JudgeCommitProofSampleRoot {
		t.Fatalf("root %s", p.RootDigest)
	}
}

func TestEscapeHTML(t *testing.T) {
	if got := EscapeHTML(`<a href="x">'&'</a>`); got != "&lt;a href=&quot;x&quot;&gt;&#039;&amp;&#039;&lt;/a&gt;" {
		t.Fatalf("%s", got)
	}
}

func TestShortDigest(t *testing.T) {
	for in, want := range map[string]string{
		"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef": "sha256:01234567...89abcdef",
		"1234567890123456789012": "1234567890123456789012",
		// UTF-16 slices: an astral character is two units.
		"\U0001F600234567890123456789012": "\U0001F6002345678901234...56789012",
	} {
		if got := shortDigest(in); got != want {
			t.Errorf("shortDigest(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRequestPathname(t *testing.T) {
	for in, want := range map[string]string{
		"/": "/", "/?a=1": "/", "/x/..": "/", "/%2e": "/", "/x/%2E%2e": "/", "/x": "/x",
		`\`: "/", "/./": "/", "/../": "/", "/x/": "/x/", "/#f": "/",
		"//host/": "/", "//host": "/", "//host/x": "/x", `\\host\`: "/",
	} {
		if got := requestPathname(in); got != want {
			t.Errorf("requestPathname(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestServerRefusesBadPort(t *testing.T) {
	for _, port := range []float64{-1, 1.5, 70000} {
		if _, err := StartGitProofServer(View{}, port); err == nil || !strings.HasPrefix(err.Error(), "options.port should be >= 0 and < 65536. Received type number (") {
			t.Errorf("port %v: %v", port, err)
		}
	}
}

// Node's http hands `OPTIONS *` to the request listener like any request,
// so TS answers 404; Go's net/http answers it itself with 200 unless told
// not to (round 7 #11).
func TestServerOptionsStarIsNotFound(t *testing.T) {
	s, err := StartGitProofServer(View{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, target := range []string{"*", "/x"} {
		conn, err := net.Dial("tcp", strings.TrimPrefix(s.URL(), "http://"))
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(conn, "OPTIONS "+target+" HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n")
		reply, _ := io.ReadAll(conn)
		conn.Close()
		head, body, _ := strings.Cut(string(reply), "\r\n\r\n")
		if !strings.HasPrefix(head, "HTTP/1.1 404 Not Found\r\n") || !strings.Contains(head, "\r\nContent-Type: text/plain; charset=utf-8\r\n") ||
			!strings.Contains(head, "\r\nX-Content-Type-Options: nosniff\r\n") || !strings.Contains(body, "Not found") {
			t.Errorf("OPTIONS %s:\n%s", target, reply)
		}
	}
}
