//go:build faultline_test

package difftest

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/demoartifactstest"
	"github.com/Mizore66/faultline/difftest/oracle"
)

// The native fault rows strace the tagged driver. Its own before/after tree
// inventories read every file of the case root (including the preview HTML the
// fault targets) inside the traced process, so a Go preview row saw foreign
// open and close records. The fault runner therefore runs Go CLI requests with
// --no-inventory and inventories the tree outside strace. These tests run the
// real driver and the real tagged fl binary with and without the flag.

const driverPreviewRequest = `{"id":"driver-preview","kind":"cli","output":"out.html","argv":["--output","out.html"]}`

type driverResult struct {
	exit           int
	stdout, stderr string
}

func buildDemoDriverForTest(t *testing.T) string {
	t.Helper()
	repo := oracle.RepoRoot(t)
	out := filepath.Join(t.TempDir(), "demo-driver")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-tags", "faultline_test", "-o", out, "./difftest/demoartifactsdriver")
	cmd.Dir = repo
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the demo driver: %v\n%s", err, b)
	}
	return out
}

func runDemoDriver(t *testing.T, driver, root string, extra ...string) driverResult {
	t.Helper()
	repo := oracle.RepoRoot(t)
	reqFile := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(reqFile, []byte(driverPreviewRequest), 0o644); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"--root", root, "--repo", repo, "--request", reqFile, "--cli", flBinary}, extra...)
	cmd := exec.Command(driver, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	res := driverResult{stdout: stdout.String(), stderr: stderr.String()}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		res.exit = exitErr.ExitCode()
	default:
		t.Fatalf("running the driver: %v", err)
	}
	return res
}

func decodeDriverCapture(t *testing.T, res driverResult) demoartifactstest.Capture {
	t.Helper()
	if res.exit != 0 {
		t.Fatalf("driver exit %d\nstdout: %.500s\nstderr: %s", res.exit, res.stdout, res.stderr)
	}
	var c demoartifactstest.Capture
	if err := json.Unmarshal([]byte(res.stdout), &c); err != nil {
		t.Fatalf("driver output: %v\n%.500s", err, res.stdout)
	}
	return c
}

func TestDemoDriverInventoryModes(t *testing.T) {
	driver := buildDemoDriverForTest(t)
	page, err := os.ReadFile(filepath.Join("testdata", "demo-artifacts", "judge-preview.html"))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("default keeps complete before and after inventories", func(t *testing.T) {
		root := demoCaseRoot(t)
		c := decodeDriverCapture(t, runDemoDriver(t, driver, root))
		external, err := demoartifactstest.Inventory(root)
		if err != nil {
			t.Fatal(err)
		}
		if c.Process == nil || c.Call != nil || c.Process.ExitCode != 0 {
			t.Fatalf("process outcome: %+v call=%+v", c.Process, c.Call)
		}
		if len(c.Before) != 0 || len(c.After) != 1 || c.After[0].Path != "out.html" {
			t.Fatalf("default inventories before=%v after=%v", c.Before, c.After)
		}
		if !reflect.DeepEqual(c.After, external) {
			t.Fatalf("driver after inventory differs from the external tree\n%v\n%v", c.After, external)
		}
		body, err := base64.StdEncoding.DecodeString(*external[0].Base64)
		if err != nil || string(body) != string(page) {
			t.Fatalf("out.html is not the committed page (%d bytes, want %d, %v)", len(body), len(page), err)
		}
	})

	t.Run("no-inventory has no inventories, the same process outcome and the same external tree", func(t *testing.T) {
		defRoot, bareRoot := demoCaseRoot(t), demoCaseRoot(t)
		def := decodeDriverCapture(t, runDemoDriver(t, driver, defRoot))
		bare := decodeDriverCapture(t, runDemoDriver(t, driver, bareRoot, "--no-inventory"))
		if bare.Before != nil || bare.After != nil {
			t.Fatalf("bare mode returned inventories before=%v after=%v", bare.Before, bare.After)
		}
		wantProc := strings.ReplaceAll(def.Process.Stdout, defRoot, "<ROOT>")
		gotProc := strings.ReplaceAll(bare.Process.Stdout, bareRoot, "<ROOT>")
		if gotProc != wantProc || bare.Process.Stderr != def.Process.Stderr || bare.Process.ExitCode != def.Process.ExitCode {
			t.Fatalf("process outcome changed by the flag\nbare: %+v\ndefault: %+v", bare.Process, def.Process)
		}
		if bare.Schema != def.Schema || !reflect.DeepEqual(bare.Request, def.Request) {
			t.Fatalf("capture header changed by the flag: %+v vs %+v", bare, def)
		}
		external, err := demoartifactstest.Inventory(bareRoot)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(external, def.After) {
			t.Fatalf("external tree after a bare run differs from the default run's after\n%v\n%v", external, def.After)
		}
	})

	// Not just discarded: bare mode must not walk or read the tree at all. A
	// FIFO in the root makes Inventory fail (an unsupported fixture entry), so a
	// bare run that still inventoried would exit 2 like the default run does.
	if runtime.GOOS != "windows" {
		t.Run("no-inventory never reads the tree", func(t *testing.T) {
			mk := func() string {
				root := demoCaseRoot(t)
				if out, err := exec.Command("mkfifo", filepath.Join(root, "pipe")).CombinedOutput(); err != nil {
					t.Fatalf("mkfifo: %v %s", err, out)
				}
				return root
			}
			if res := runDemoDriver(t, driver, mk()); res.exit != 2 {
				t.Fatalf("default mode with an unsupported entry: exit %d, want the adapter failure 2 (stderr %q)", res.exit, res.stderr)
			}
			c := decodeDriverCapture(t, runDemoDriver(t, driver, mk(), "--no-inventory"))
			if c.Process == nil || c.Process.ExitCode != 0 || c.Before != nil || c.After != nil {
				t.Fatalf("bare run with an unreadable tree: %+v", c)
			}
		})
	}
}
