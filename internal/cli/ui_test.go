package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/proofview"
)

type uiFake struct {
	artifacts []proofview.DiscoveredFaultLineArtifact
	closes    int
}

func (f *uiFake) URL() string { return "http://127.0.0.1:43210" }
func (f *uiFake) Close() error {
	f.closes++
	return nil
}
func (f *uiFake) Artifacts() []proofview.DiscoveredFaultLineArtifact { return f.artifacts }

type uiCalls struct {
	repository string
	port       float64
	starts     int
	opened     []string
	fake       *uiFake
}

func installUIFake(t *testing.T, artifacts int, startErr error) *uiCalls {
	t.Helper()
	c := &uiCalls{fake: &uiFake{}}
	for i := 0; i < artifacts; i++ {
		c.fake.artifacts = append(c.fake.artifacts, proofview.DiscoveredFaultLineArtifact{Kind: "ledger"})
	}
	oStart, oOpen, oAwait := uiStartServer, uiOpenBrowser, uiAwaitInterrupt
	uiStartServer = func(repository string, port float64) (uiServer, error) {
		c.starts++
		c.repository, c.port = repository, port
		if startErr != nil {
			return nil, startErr
		}
		return c.fake, nil
	}
	uiOpenBrowser = func(_ *env, url string) { c.opened = append(c.opened, url) }
	uiAwaitInterrupt = func(start func() (func() error, error)) error {
		closeServer, err := start()
		if err != nil {
			return err
		}
		return closeServer()
	}
	t.Cleanup(func() { uiStartServer, uiOpenBrowser, uiAwaitInterrupt = oStart, oOpen, oAwait })
	return c
}

func uiStartup(n string) string {
	return "FaultLine UI hub: http://127.0.0.1:43210\nDiscovered " + n + " local artifact(s) under .faultline\nPress Ctrl+C to stop.\n"
}

func TestUICommand(t *testing.T) {
	t.Run("default repository and port, empty", func(t *testing.T) {
		dir, _ := filepath.EvalSymlinks(t.TempDir())
		t.Chdir(dir)
		c := installUIFake(t, 0, nil)
		stdout, stderr, code := run("ui")
		if code != 0 || stdout != uiStartup("0") || stderr != "Next: fl quickstart\n" {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
		cwd, _ := os.Getwd()
		if c.repository != cwd || c.port != 4173 || c.starts != 1 || c.fake.closes != 1 {
			t.Fatalf("%+v", c)
		}
		if len(c.opened) != 1 || c.opened[0] != "http://127.0.0.1:43210" {
			t.Fatalf("opened %v", c.opened)
		}
	})
	t.Run("custom repository, nonempty, port zero", func(t *testing.T) {
		dir, _ := filepath.EvalSymlinks(t.TempDir())
		t.Chdir(dir)
		c := installUIFake(t, 2, nil)
		stdout, stderr, code := run("ui", "--repo", "sub/../repo", "--port", "0")
		want := filepath.Join(dir, "repo")
		if code != 0 || stdout != uiStartup("2") || stderr != "Next: fl ui --repo "+want+"\n" {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
		if c.repository != want || c.port != 0 || c.fake.closes != 1 {
			t.Fatalf("%+v", c)
		}
	})
	t.Run("invalid ports", func(t *testing.T) {
		for _, port := range []string{"abc", "-1", "65536", "1.5", "Infinity", "NaN", "1e9"} {
			c := installUIFake(t, 0, nil)
			stdout, stderr, code := run("ui", "--port", port)
			if code != 1 || stdout != "" || !strings.HasPrefix(stderr, "FaultLine error: "+uiDiagnostic(t, "ui-invalid-port")+"\n") || c.starts != 0 || len(c.opened) != 0 {
				t.Fatalf("port %q: exit=%d stdout=%q stderr=%q starts=%d", port, code, stdout, stderr, c.starts)
			}
		}
	})
	t.Run("start failure prints nothing and does not open", func(t *testing.T) {
		c := installUIFake(t, 0, errors.New("listen EADDRINUSE: address already in use 127.0.0.1:1"))
		stdout, stderr, code := run("ui", "--port", "1")
		if code != 1 || stdout != "" || !strings.HasPrefix(stderr, "FaultLine error: listen EADDRINUSE") || len(c.opened) != 0 {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
	})
	t.Run("real discovery count", func(t *testing.T) {
		dir, _ := filepath.EvalSymlinks(t.TempDir())
		drafts := filepath.Join(dir, ".faultline", "incidents", "drafts")
		if err := os.MkdirAll(drafts, 0o700); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(drafts, "a.json"), []byte("{}"), 0o600)
		os.WriteFile(filepath.Join(drafts, "b.json"), []byte("{}"), 0o600)
		hub, err := proofview.StartUIHubServer(dir, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer hub.Close()
		if n := len(hub.Artifacts()); n != 2 {
			t.Fatalf("discovered %d", n)
		}
	})
}

// uiDiagnostic returns one hand-committed expectation from
// ui-diagnostics.jsonl, never generated from the code under test.
func uiDiagnostic(t *testing.T, id string) string {
	t.Helper()
	raw, err := os.ReadFile("../../difftest/testdata/ui-hub/ui-diagnostics.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		var row struct{ ID, Text string }
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		if row.ID == id {
			return row.Text
		}
	}
	t.Fatalf("no committed diagnostic %q", id)
	return ""
}
