package nativefixture

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if code, handled := dispatchProcessTestHelper(os.Args[1:]); handled {
		os.Exit(code)
	}
	if code, handled := DispatchChild(os.Args[1:], os.Getenv(ChildEnvName), os.Stdout, os.Stderr); handled {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

const (
	wantPassOut   = "settlement currency preserved\n{\"protocol\":\"faultline.witness-result.v1\",\"outcome\":\"PREDICATE_PASS\"}\n"
	wantFailOut   = "{\"protocol\":\"faultline.witness-result.v1\",\"outcome\":\"PREDICATE_FAIL\"}\n"
	wantFailErr   = "expected settlement USD; received EUR\n"
	validConfig   = "{\"displayDefault\":\"ORDER_CURRENCY\",\"settlementSource\":\"ORDER_CURRENCY\"}\n"
	partialConfig = "{\"displayDefault\":\"ORDER_CURRENCY\",\"settlementSource\":}\n"
)

// Actual self-exec of the test executable through the production dispatch.
// This helper is not the future production process runner.
func runChildDirectory(t *testing.T, dir string) (int, string, string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "--internal-native-demo-witness-v1")
	cmd.Dir = dir
	cmd.Env = []string{"FAULTLINE_NATIVE_DEMO_CHILD=v1"}
	if runtime.GOOS == "windows" {
		cmd.Env = append(cmd.Env, "SYSTEMROOT="+os.Getenv("SYSTEMROOT"))
	}
	cmd.WaitDelay = 250 * time.Millisecond
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	var exited *exec.ExitError
	if ctx.Err() != nil || (err != nil && !errors.As(err, &exited)) || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() < 0 {
		t.Fatalf("self-exec err=%v context=%v stdout=%q stderr=%q", err, ctx.Err(), stdout.String(), stderr.String())
	}
	return cmd.ProcessState.ExitCode(), stdout.String(), stderr.String()
}

func runChildConfig(t *testing.T, data string) (int, string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "native-config.json"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return runChildDirectory(t, dir)
}

// Independent token-stream probe: no schema, materializer, or model validation.
// Decode has different diagnostics; the approved child uses Decoder.Token.
func tokenSyntaxCause(t *testing.T, data string) string {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(data))
	for {
		// More's lookahead participates in the standard library's EOF diagnostics.
		decoder.More()
		_, err := decoder.Token()
		if err == io.EOF {
			t.Fatal("independent malformed JSON probe unexpectedly reached clean EOF")
		}
		if err != nil {
			return err.Error()
		}
	}
}

func partialDiagnostic(t *testing.T) string {
	t.Helper()
	return "native fixture config: " + tokenSyntaxCause(t, partialConfig) + "\n"
}

func assertChild(t *testing.T, data string, wantCode int, wantOut, wantErr string) {
	t.Helper()
	code, stdout, stderr := runChildConfig(t, data)
	if code != wantCode || stdout != wantOut || stderr != wantErr {
		t.Fatalf("child status=%d stdout=%q stderr=%q; want status=%d stdout=%q stderr=%q", code, stdout, stderr, wantCode, wantOut, wantErr)
	}
}

func TestNativeChildExactProtocol(t *testing.T) {
	for _, tc := range []struct {
		name, data     string
		status         int
		stdout, stderr string
	}{
		{"none", validConfig, 0, wantPassOut, ""},
		{"source", "{\"displayDefault\":\"EUR\",\"settlementSource\":\"ORDER_CURRENCY\"}\n", 0, wantPassOut, ""},
		{"boundary", "{\"displayDefault\":\"ORDER_CURRENCY\",\"settlementSource\":\"DISPLAY_CURRENCY\"}\n", 0, wantPassOut, ""},
		{"both", "{\"displayDefault\":\"EUR\",\"settlementSource\":\"DISPLAY_CURRENCY\"}\n", 1, wantFailOut, wantFailErr},
		{"partial", partialConfig, 3, "", partialDiagnostic(t)},
	} {
		t.Run(tc.name, func(t *testing.T) { assertChild(t, tc.data, tc.status, tc.stdout, tc.stderr) })
	}
	t.Logf("independent token-decoder probe under %s: %q", runtime.Version(), partialDiagnostic(t))
}

func TestNativeChildAllLogicalStates(t *testing.T) {
	seen := make(map[string]bool)
	for _, capture := range referenceInput(t).Get("captures").Items() {
		state := capture.Get("state")
		id := state.Get("id").Str()
		if seen[id] {
			continue
		}
		seen[id] = true
		t.Run(id, func(t *testing.T) {
			var hunks []string
			for _, h := range state.Get("hunks").Items() {
				hunks = append(hunks, h.Str())
			}
			config, err := Materialize(hunks)
			if err != nil {
				t.Fatal(err)
			}
			wantConfig, err := base64.StdEncoding.Strict().DecodeString(capture.Get("config", "data").Str())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(config.Data, wantConfig) || config.Partial != capture.Get("expectedPartial").Bool() {
				t.Fatal("complete materialization differs from independent reference")
			}
			wantOut, err := base64.StdEncoding.Strict().DecodeString(capture.Get("stdout", "data").Str())
			if err != nil {
				t.Fatal(err)
			}
			wantErr, err := base64.StdEncoding.Strict().DecodeString(capture.Get("stderr", "data").Str())
			if err != nil {
				t.Fatal(err)
			}
			if config.Partial {
				wantErr = []byte(partialDiagnostic(t))
			}
			assertChild(t, string(config.Data), int(capture.Get("status").Num()), string(wantOut), string(wantErr))
		})
	}
	if len(seen) != 20 {
		t.Fatalf("logical state count=%d want20", len(seen))
	}
}

func TestNativeChildStrictConfig(t *testing.T) {
	for _, tc := range []struct{ name, data, cause string }{
		{"empty", "", "EOF"},
		{"array", "[]", "expected configuration object"},
		{"null", "null", "expected configuration object"},
		{"number", "1", "expected configuration object"},
		{"bool", "true", "expected configuration object"},
		{"string", `"config"`, "expected configuration object"},
		{"missing-both", "{}", `missing field "displayDefault"`},
		{"missing-display", `{"settlementSource":"ORDER_CURRENCY"}`, `missing field "displayDefault"`},
		{"missing-settlement", `{"displayDefault":"ORDER_CURRENCY"}`, `missing field "settlementSource"`},
		{"duplicate-display", `{"displayDefault":"EUR","displayDefault":"EUR","settlementSource":"ORDER_CURRENCY"}`, `duplicate field "displayDefault"`},
		{"duplicate-escaped", strings.Replace(`{"displayDefault":"EUR","displayDefault":"EUR","settlementSource":"ORDER_CURRENCY"}`, "displayDefault", "display"+string(rune(92))+"u0044efault", 1), `duplicate field "displayDefault"`},
		{"duplicate-settlement", `{"displayDefault":"EUR","settlementSource":"ORDER_CURRENCY","settlementSource":"ORDER_CURRENCY"}`, `duplicate field "settlementSource"`},
		{"unknown", `{"other":"EUR"}`, `unknown field "other"`},
		{"unknown-newline", "{\"other\\nfield\":\"EUR\"}", `unknown field "other\nfield"`},
		{"display-null", `{"displayDefault":null,"settlementSource":"ORDER_CURRENCY"}`, `field "displayDefault" must be a string`},
		{"display-bool", `{"displayDefault":false,"settlementSource":"ORDER_CURRENCY"}`, `field "displayDefault" must be a string`},
		{"display-number", `{"displayDefault":1,"settlementSource":"ORDER_CURRENCY"}`, `field "displayDefault" must be a string`},
		{"display-array", `{"displayDefault":[],"settlementSource":"ORDER_CURRENCY"}`, `field "displayDefault" must be a string`},
		{"display-object", `{"displayDefault":{},"settlementSource":"ORDER_CURRENCY"}`, `field "displayDefault" must be a string`},
		{"settlement-null", `{"displayDefault":"EUR","settlementSource":null}`, `field "settlementSource" must be a string`},
		{"bad-display-enum", `{"displayDefault":"USD","settlementSource":"ORDER_CURRENCY"}`, `invalid displayDefault "USD"`},
		{"bad-settlement-enum", `{"displayDefault":"EUR","settlementSource":"EUR"}`, `invalid settlementSource "EUR"`},
		{"bad-display-newline", "{\"displayDefault\":\"EUR\\r\\n\",\"settlementSource\":\"ORDER_CURRENCY\"}", `invalid displayDefault "EUR\r\n"`},
		{"trailing-object", validConfig + "{}", "unexpected data after configuration object"},
		{"trailing-null", validConfig + "null", "unexpected data after configuration object"},
		{"trailing-string", validConfig + `"extra"`, "unexpected data after configuration object"},
		{"unclosed", `{"displayDefault":"ORDER_CURRENCY"`, tokenSyntaxCause(t, `{"displayDefault":"ORDER_CURRENCY"`)},
		{"invalid-utf8", "{\"displayDefault\":\"\xff\",\"settlementSource\":\"ORDER_CURRENCY\"}", "configuration is not valid UTF-8"},
		{"oversize", validConfig + strings.Repeat(" ", 16385-len(validConfig)), "configuration exceeds 16384 bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) { assertChild(t, tc.data, 3, "", "native fixture config: "+tc.cause+"\n") })
	}
}

func TestNativeChildValidConfigOrderAndSizeBoundary(t *testing.T) {
	for _, tc := range []struct{ name, data string }{
		{"reversed", "  {\"settlementSource\":\"ORDER_CURRENCY\",\"displayDefault\":\"EUR\"}\r\n\t "},
		{"escaped-known-key", strings.Replace(validConfig, "displayDefault", "display"+string(rune(92))+"u0044efault", 1)},
		{"exact-16KiB", validConfig + strings.Repeat(" ", 16384-len(validConfig))},
	} {
		t.Run(tc.name, func(t *testing.T) { assertChild(t, tc.data, 0, wantPassOut, "") })
	}
}

func TestNativeChildConfigEntry(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		dir := t.TempDir()
		_, err := os.Lstat(filepath.Join(dir, "native-config.json"))
		var pathErr *os.PathError
		if !errors.As(err, &pathErr) {
			t.Fatalf("missing-file probe: %v", err)
		}
		pathErr.Path = "native-config.json"
		code, stdout, stderr := runChildDirectory(t, dir)
		if code != 3 || stdout != "" || stderr != "native fixture config: "+pathErr.Error()+"\n" {
			t.Fatalf("status=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
	})
	t.Run("directory", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "native-config.json"), 0700); err != nil {
			t.Fatal(err)
		}
		code, stdout, stderr := runChildDirectory(t, dir)
		if code != 3 || stdout != "" || stderr != "native fixture config: native-config.json is not a regular file\n" {
			t.Fatalf("status=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(t.TempDir(), "caller-owned.json")
		if err := os.WriteFile(target, []byte(validConfig), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, "native-config.json")); err != nil {
			t.Fatal(err)
		}
		code, stdout, stderr := runChildDirectory(t, dir)
		if code != 3 || stdout != "" || stderr != "native fixture config: native-config.json must not be a symlink\n" {
			t.Fatalf("status=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
		data, err := os.ReadFile(target)
		if err != nil || string(data) != validConfig {
			t.Fatal("caller-owned symlink target changed")
		}
	})
}

func TestNativeDispatchIgnoresOtherInvocations(t *testing.T) {
	t.Chdir(t.TempDir())
	for i, tc := range []struct {
		args   []string
		marker string
	}{
		{nil, "v1"}, {[]string{"--internal-native-demo-witness-v1"}, ""},
		{[]string{"--internal-native-demo-witness-v1"}, "V1"},
		{[]string{"--internal-native-demo-witness-v1", "--help"}, "v1"},
		{[]string{"--help", "--internal-native-demo-witness-v1"}, "v1"},
		{[]string{"--internal-native-demo-witness-v1", "--internal-native-demo-witness-v1"}, "v1"},
		{[]string{"--internal-native-demo-witness-v2"}, "v1"},
		{[]string{"--help"}, "v1"},
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code, handled := DispatchChild(tc.args, tc.marker, &stdout, &stderr)
			if handled || code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("handled=%v code=%d stdout=%q stderr=%q", handled, code, stdout.String(), stderr.String())
			}
		})
	}
}

type brokenWriter struct{ short bool }

func (w brokenWriter) Write(p []byte) (int, error) {
	if w.short {
		return len(p) - 1, nil
	}
	return 0, errors.New("test writer failure")
}

func TestNativeChildWriterFailuresAreNotAccepted(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		stderr     bool
	}{
		{"pass-stdout", validConfig, false},
		{"fail-stdout", `{"displayDefault":"EUR","settlementSource":"DISPLAY_CURRENCY"}`, false},
		{"fail-stderr", `{"displayDefault":"EUR","settlementSource":"DISPLAY_CURRENCY"}`, true},
		{"config-stderr", partialConfig, true},
	} {
		for _, short := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/short=%v", tc.name, short), func(t *testing.T) {
				dir := t.TempDir()
				t.Chdir(dir)
				if err := os.WriteFile("native-config.json", []byte(tc.data), 0600); err != nil {
					t.Fatal(err)
				}
				var stdout, stderr io.Writer = io.Discard, io.Discard
				if tc.stderr {
					stderr = brokenWriter{short}
				} else {
					stdout = brokenWriter{short}
				}
				code, handled := DispatchChild([]string{"--internal-native-demo-witness-v1"}, "v1", stdout, stderr)
				if !handled || code != 2 {
					t.Fatalf("writer failure handled=%v status=%d;want abnormal2", handled, code)
				}
			})
		}
	}
}
