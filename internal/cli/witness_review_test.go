package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/proofview"
)

type reviewServerFake struct{ url string }

func (f reviewServerFake) URL() string  { return f.url }
func (f reviewServerFake) Close() error { return nil }

type reviewFakes struct {
	mode        string
	options     proofview.WitnessReviewServerOptions
	port        float64
	ttyDraftNil bool
}

// installReviewFakes replaces every package-private seam and restores them in
// t.Cleanup. The TTY seam mimics RunWitnessReviewTTY's non-TTY refusal.
func installReviewFakes(t *testing.T, stdinTTY, stdoutTTY bool) *reviewFakes {
	t.Helper()
	f := &reviewFakes{}
	oTerm, oTTY, oStart, oAwait := witnessReviewTerminals, witnessReviewRunTTY, witnessReviewStartServer, witnessReviewAwait
	witnessReviewTerminals = func() (bool, bool) { return stdinTTY, stdoutTTY }
	witnessReviewRunTTY = func(o WitnessReviewTTYOptions) error {
		f.ttyDraftNil = o.Draft == nil
		if !o.StdinIsTTY || !o.StdoutIsTTY {
			f.mode = "tty-refused"
			return &WitnessReviewTTYError{"NON_TTY", "refused"}
		}
		f.mode = "tty"
		return nil
	}
	witnessReviewStartServer = func(o proofview.WitnessReviewServerOptions, port float64) (witnessReviewServer, error) {
		f.options, f.port = o, port
		if f.mode == "tty-refused" {
			f.mode = "server-fallback"
		} else {
			f.mode = "server"
		}
		return reviewServerFake{"http://127.0.0.1:43210"}, nil
	}
	witnessReviewAwait = func(start func() (func() error, error)) error {
		closeServer, err := start()
		if err != nil {
			return err
		}
		return closeServer()
	}
	t.Cleanup(func() {
		witnessReviewTerminals, witnessReviewRunTTY, witnessReviewStartServer, witnessReviewAwait = oTerm, oTTY, oStart, oAwait
	})
	return f
}

func runWitnessReviewWithFakes(t *testing.T, args []string, stdinTTY, stdoutTTY bool) (mode, stdout, stderr string, exitCode int) {
	t.Helper()
	t.Chdir(t.TempDir())
	f := installReviewFakes(t, stdinTTY, stdoutTTY)
	store := ttyProposal(t, "p1")
	var out, errb bytes.Buffer
	code := Run(append([]string{"witness", "review", "p1", "--store", store}, args...), &out, &errb)
	mode = f.mode
	if strings.HasPrefix(out.String(), "{\n") {
		mode = "json"
	}
	return mode, out.String(), errb.String(), code
}

func TestWitnessReviewCommand(t *testing.T) {
	for _, tc := range []struct {
		name                string
		args                []string
		stdinTTY, stdoutTTY bool
		wantMode            string
		wantExit            int
	}{
		{"json ignores bad port", []string{"--json", "--port", "bad"}, false, false, "json", 0},
		{"tty wins browser", []string{"--tty", "--browser"}, true, true, "tty", 0},
		{"port forces browser", []string{"--port", "0"}, true, true, "server", 0},
		{"interactive default", nil, true, true, "tty", 0},
		{"stdin pipe falls back", nil, false, true, "server", 0},
		{"stdout pipe falls back", nil, true, false, "server", 0},
		{"forced tty fallback exit", []string{"--tty"}, false, true, "server-fallback", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mode, stdout, stderr, code := runWitnessReviewWithFakes(t, tc.args, tc.stdinTTY, tc.stdoutTTY)
			if mode != tc.wantMode || code != tc.wantExit {
				t.Fatalf("mode=%q want %q; exit=%d want %d; stdout=%q stderr=%q", mode, tc.wantMode, code, tc.wantExit, stdout, stderr)
			}
		})
	}

	t.Run("json shape and no server", func(t *testing.T) {
		_, stdout, stderr, code := runWitnessReviewWithFakes(t, []string{"--json"}, true, true)
		if code != 0 || stderr != "" || !strings.HasSuffix(stdout, "\n") {
			t.Fatalf("exit=%d stderr=%q", code, stderr)
		}
		v, err := jsjson.Parse(stdout)
		if err != nil {
			t.Fatal(err)
		}
		keys := v.Obj().Keys()
		if len(keys) != 3 || keys[0] != "status" || keys[1] != "review" || keys[2] != "sensitivity" {
			t.Fatalf("keys %v", keys)
		}
		if !strings.HasPrefix(stdout, "{\n  \"status\": \"LOCAL_READ_ONLY_REVIEW\",\n  \"review\": {\n") {
			t.Fatalf("indentation: %q", stdout[:80])
		}
		if !strings.Contains(stdout, `"ciLog": "Expected USD; received EUR."`) {
			t.Fatal("ciLog missing from JSON")
		}
	})

	t.Run("server readiness text", func(t *testing.T) {
		_, stdout, stderr, code := runWitnessReviewWithFakes(t, nil, false, false)
		if code != 0 || stderr != "" || stdout != "FaultLine local witness review: http://127.0.0.1:43210\nApprove & freeze is one action. Press Ctrl+C to stop.\n" {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
		_, stdout, _, _ = runWitnessReviewWithFakes(t, []string{"--two-step"}, false, false)
		if stdout != "FaultLine local witness review: http://127.0.0.1:43210\nTwo-step mode: Approve, then Freeze (separate clicks). Press Ctrl+C to stop.\n" {
			t.Fatalf("two-step stdout=%q", stdout)
		}
	})

	t.Run("two-step environment", func(t *testing.T) {
		t.Setenv("FAULTLINE_WITNESS_TWO_STEP", "1")
		_, stdout, _, _ := runWitnessReviewWithFakes(t, nil, false, false)
		if !strings.Contains(stdout, "Two-step mode") {
			t.Fatalf("stdout=%q", stdout)
		}
	})

	t.Run("forced tty fallback text", func(t *testing.T) {
		_, stdout, stderr, code := runWitnessReviewWithFakes(t, []string{"--tty"}, false, true)
		want := NonTtyReviewRefusalMessage("http://127.0.0.1:43210") + "\n"
		if code != 1 || stderr != want || stdout != "FaultLine local witness review: http://127.0.0.1:43210\nPress Ctrl+C to stop.\n" {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
	})

	t.Run("port and draft handling", func(t *testing.T) {
		_, _, stderr, code := runWitnessReviewWithFakes(t, []string{"--port", "70000"}, true, true)
		if code != 1 || stderr != "FaultLine error: "+reviewDiagnostic(t, "witness-review-invalid-port")+"\nNext: pnpm fl help\n" {
			t.Fatalf("exit=%d stderr=%q", code, stderr)
		}
		t.Chdir(t.TempDir())
		f := installReviewFakes(t, false, false)
		var out, errb bytes.Buffer
		store := ttyProposal(t, "p1")
		if code := Run([]string{"witness", "review", "p1", "--store", store, "--port", "0x10", "--draft-store", "d"}, &out, &errb); code != 0 {
			t.Fatalf("exit=%d %q", code, errb.String())
		}
		if f.port != 16 || !strings.HasSuffix(f.options.DraftStore, "/d") || f.options.ProposalID != "p1" {
			t.Fatalf("%+v", f)
		}
	})

	t.Run("missing proposal id and unknown proposal", func(t *testing.T) {
		installReviewFakes(t, false, false)
		var out, errb bytes.Buffer
		if code := Run([]string{"witness", "review"}, &out, &errb); code != 1 ||
			errb.String() != "FaultLine error: "+reviewDiagnostic(t, "witness-review-usage")+"\nNext: pnpm fl help\n" {
			t.Fatalf("%q", errb.String())
		}
	})

	t.Run("tty draft read failure means no draft", func(t *testing.T) {
		t.Chdir(t.TempDir())
		f := installReviewFakes(t, true, true)
		store := ttyProposal(t, "p1")
		var out, errb bytes.Buffer
		if code := Run([]string{"witness", "review", "p1", "--store", store}, &out, &errb); code != 0 || !f.ttyDraftNil {
			t.Fatalf("exit=%d draftNil=%v %q", code, f.ttyDraftNil, errb.String())
		}
	})
}

// reviewDiagnosticsFile is absolute: several tests change directory.
var reviewDiagnosticsFile, _ = filepath.Abs("../../difftest/testdata/witness-review/cli-diagnostics.jsonl")

// reviewDiagnostic returns one hand-committed expectation from
// cli-diagnostics.jsonl, never generated from the code under test.
func reviewDiagnostic(t *testing.T, id string) string {
	t.Helper()
	raw, err := os.ReadFile(reviewDiagnosticsFile)
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
