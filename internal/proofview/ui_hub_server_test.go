package proofview

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func copyHubTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func uiHubExchange(t *testing.T, s *UIHubServer, request string) incidentReply {
	t.Helper()
	conn, err := net.Dial("tcp", strings.TrimPrefix(s.URL(), "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Git verification takes seconds, longer than rawExchange allows.
	conn.SetDeadline(time.Now().Add(60 * time.Second))
	io.WriteString(conn, request)
	data, _ := io.ReadAll(conn)
	raw := string(data)
	head, body, _ := strings.Cut(raw, "\r\n\r\n")
	return incidentReply{head: head, body: body}
}

func hubGet(path string) string {
	return "GET " + path + " HTTP/1.1\r\nHost: example.invalid\r\nConnection: close\r\n\r\n"
}

func hubFixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	fl := filepath.Join(repo, ".faultline")
	copyHubTree(t, filepath.Join("..", "..", "difftest", "testdata", "serve-bundle", "inputs", "coherent", "bundle"), filepath.Join(fl, "git-proof-bundles", "git"))
	copyHubTree(t, filepath.Join("..", "..", "difftest", "testdata", "bases", "turn-two-states"), filepath.Join(fl, "turn-proof-bundles", "turn"))
	writeHubFile(t, filepath.Join(fl, "incidents", "drafts", "d.json"), `{"a":"<b>"}`)
	writeHubFile(t, filepath.Join(fl, "recordings", "l.json"), `{"ledger":1}`)
	return repo
}

func TestUIHubServerHTTP(t *testing.T) {
	repo := hubFixture(t)
	s, err := StartUIHubServer(repo, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	arts := s.Artifacts()
	if len(arts) != 4 || arts[0].Kind != "git-proof-bundle" || arts[1].Kind != "turn-proof-bundle" {
		t.Fatalf("artifacts %v", arts)
	}

	index := uiHubExchange(t, s, hubGet("/?x=1"))
	if index.status() != "HTTP/1.1 200 OK" || index.decoded() != RenderHubIndex(repo, arts) ||
		!index.has("Cache-Control: no-store") || !index.has("Content-Type: text/html; charset=utf-8") {
		t.Fatalf("index: %s", index.head)
	}

	git := uiHubExchange(t, s, hubGet("/artifact/0"))
	view, err := LoadVerifiedGitProofView(arts[0].Path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if git.status() != "HTTP/1.1 200 OK" || git.decoded() != RenderGitProofIncidentPage(view) || !git.has("Cache-Control: no-store") {
		t.Fatalf("git artifact: %s", git.head)
	}

	turn := uiHubExchange(t, s, hubGet("/artifact/1"))
	if turn.status() != "HTTP/1.1 200 OK" || !turn.has("Content-Type: text/html; charset=utf-8") ||
		!turn.has("Cache-Control: no-store") || strings.Contains(turn.decoded(), "failed verification") || len(turn.decoded()) < 100 {
		t.Fatalf("turn artifact: %s\n%s", turn.head, turn.decoded())
	}

	for _, p := range []string{"/raw/1", "/raw/2", "/raw/3"} {
		raw := uiHubExchange(t, s, hubGet(p))
		if raw.status() != "HTTP/1.1 200 OK" || !raw.has("Content-Type: text/html; charset=utf-8") || !raw.has("Cache-Control: no-store") {
			t.Fatalf("%s: %s", p, raw.head)
		}
	}
	if r := uiHubExchange(t, s, hubGet("/raw/1")); !strings.Contains(r.decoded(), "&lt;") && !strings.Contains(r.decoded(), "&quot;") {
		t.Fatalf("raw turn not escaped: %s", r.decoded())
	}
	if r := uiHubExchange(t, s, hubGet("/raw/2")); !strings.Contains(r.decoded(), "&lt;b&gt;") {
		t.Fatalf("raw draft: %s", r.decoded())
	}

	for _, c := range []struct{ path, body string }{
		{"/raw/0", "Artifact not found"}, {"/artifact/2", "Artifact not found"}, {"/artifact/3", "Artifact not found"},
		{"/artifact/9", "Artifact not found"}, {"/raw/9", "Artifact not found"},
		{"/artifact/x", "Not found"}, {"/artifact/-1", "Not found"}, {"/artifact/", "Not found"}, {"/nope", "Not found"},
	} {
		r := uiHubExchange(t, s, hubGet(c.path))
		if r.status() != "HTTP/1.1 404 Not Found" || r.decoded() != c.body || !r.has("Content-Type: text/plain; charset=utf-8") || !r.has("Cache-Control: no-store") {
			t.Fatalf("%s: %s %q", c.path, r.head, r.decoded())
		}
	}
	if r := uiHubExchange(t, s, "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"); r.status() != "HTTP/1.1 404 Not Found" {
		t.Fatalf("post: %s", r.head)
	}
}

func TestUIHubServerInvalidTurn(t *testing.T) {
	repo := hubFixture(t)
	inv := filepath.Join(repo, ".faultline", "turn-proof-bundles", "turn", "investigation.json")
	if err := os.WriteFile(inv, []byte(`{"tampered":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := StartUIHubServer(repo, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, p := range []string{"/artifact/1", "/raw/1"} {
		r := uiHubExchange(t, s, hubGet(p))
		if !strings.Contains(r.decoded(), "Turn package failed verification") || strings.Contains(r.decoded(), "<main") ||
			!r.has("Content-Type: text/html; charset=utf-8") || !r.has("Cache-Control: no-store") {
			t.Fatalf("%s: %s\n%s", p, r.head, r.decoded())
		}
	}
}
