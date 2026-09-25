// Package oracle talks to difftest/gen/node-oracle.ts (frozen TS).
package oracle

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type Client struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	nextID int
}

// RepoRoot returns the directory containing go.mod.
func RepoRoot(t testing.TB) string {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// Start launches the oracle; the test is skipped unless FAULTLINE_NODE_ORACLE=1.
func Start(t testing.TB) *Client {
	t.Helper()
	if os.Getenv("FAULTLINE_NODE_ORACLE") != "1" {
		t.Skip("set FAULTLINE_NODE_ORACLE=1 to run live oracle tests")
	}
	cmd := exec.Command("pnpm", "exec", "tsx", "difftest/gen/node-oracle.ts")
	cmd.Dir = RepoRoot(t)
	cmd.Env = append(os.Environ(), "LC_ALL=C.UTF-8")
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return &Client{cmd: cmd, stdin: stdin, stdout: bufio.NewReaderSize(stdout, 1<<20)}
}

// Call sends op with args (JSON source text) and returns result as JSON source text.
func (c *Client) Call(op, args string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	prefix := fmt.Sprintf(`{"id":%d,`, c.nextID)
	if _, err := fmt.Fprintf(c.stdin, `%s"op":%q,"args":%s}`+"\n", prefix, op, args); err != nil {
		return "", err
	}
	line, err := c.stdout.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimSuffix(line, "\n")
	if rest, ok := strings.CutPrefix(line, prefix+`"ok":true,"result":`); ok {
		return strings.TrimSuffix(rest, "}"), nil
	}
	if rest, ok := strings.CutPrefix(line, prefix+`"ok":false,"error":`); ok {
		return "", errors.New(strings.TrimSuffix(rest, "}"))
	}
	return "", fmt.Errorf("unexpected oracle response: %s", line)
}

func (c *Client) Close() {
	c.stdin.Close()
	c.cmd.Wait()
}
