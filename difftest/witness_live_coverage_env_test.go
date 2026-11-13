//go:build faultline_test

package difftest

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Dropping GOCOVERDIR adds stderr noise and loses the instrumented Go CLI's
// counters. The frozen TypeScript side must retain its original environment.
func TestLiveWitnessOracleCoverageEnv(t *testing.T) {
	coverDir := filepath.Join(t.TempDir(), "coverage = counters")
	for _, tc := range []struct {
		name, value, hostName string
		unset, wantGo         bool
	}{
		{name: "configured", value: coverDir, wantGo: true},
		{name: "empty"},
		{name: "unset", unset: true},
		{name: "lowercase_host", value: coverDir, unset: true, hostName: "gocoverdir", wantGo: runtime.GOOS == "windows"},
		{name: "mixed_case_host", value: coverDir, unset: true, hostName: "GoCoverDir", wantGo: runtime.GOOS == "windows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GOCOVERDIR", tc.value)
			if tc.unset {
				if err := os.Unsetenv("GOCOVERDIR"); err != nil {
					t.Fatal(err)
				}
			}
			if tc.hostName != "" {
				// Windows resolves aliases natively; POSIX must not inherit them.
				t.Setenv(tc.hostName, tc.value)
			}
			for _, kind := range []string{"go", "ts"} {
				t.Run(kind, func(t *testing.T) {
					side := newWloSide(t, kind, "unused-node", wloCase{})
					var values []string
					for _, entry := range side.cmd(nil).Env {
						if name, value, _ := strings.Cut(entry, "="); strings.EqualFold(name, "GOCOVERDIR") {
							if name != "GOCOVERDIR" {
								t.Errorf("coverage environment name %q is not canonical GOCOVERDIR", name)
							}
							values = append(values, value)
						}
					}
					if kind == "go" && tc.wantGo {
						if len(values) != 1 || values[0] != coverDir {
							t.Fatalf("GOCOVERDIR values = %q; want exactly [%q]", values, coverDir)
						}
					} else if len(values) != 0 {
						t.Fatalf("unexpected GOCOVERDIR values: %q", values)
					}
				})
			}
		})
	}
}

// Coverage forwarding must not become broad environment inheritance, leak a
// host API key into no-key cases, or replace the isolated HOME/TMP directories.
func TestLiveWitnessOracleEnvIsolation(t *testing.T) {
	// Exercise the Windows allowlist even on non-Windows hosts. These commands
	// are only constructed, so the synthetic paths are never executed.
	t.Setenv("SystemRoot", filepath.Join(t.TempDir(), "windows"))
	t.Setenv("SYSTEMROOT", os.Getenv("SystemRoot"))
	t.Setenv("PATHEXT", ".EXE;.CMD")
	t.Setenv("ComSpec", filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"))
	for _, host := range []struct{ name, key string }{
		{name: "unset"},
		{name: "test_key", key: "fl-test-key"},
		{name: "synthetic_real_looking_key", key: "sk-" + "proj-" + strings.Repeat("fl-test-key", 8)},
	} {
		t.Run(host.name, func(t *testing.T) {
			t.Setenv("OPENAI_API_KEY", host.key)
			if host.key == "" {
				if err := os.Unsetenv("OPENAI_API_KEY"); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("FAULTLINE_WLO_HOST_ONLY", "must-not-inherit")
			for _, kind := range []string{"go", "ts"} {
				for _, mode := range []struct {
					name  string
					noKey bool
				}{{name: "key"}, {name: "no_key", noKey: true}} {
					t.Run(kind+"/"+mode.name, func(t *testing.T) {
						side := newWloSide(t, kind, "unused-node", wloCase{noKey: mode.noKey})
						home, tmp := filepath.Join(side.root, "home"), filepath.Join(side.root, "tmp")
						want := map[string]string{
							"PATH": os.Getenv("PATH"), "HOME": home, "USERPROFILE": home,
							"TMPDIR": tmp, "TEMP": tmp, "TMP": tmp,
							"LC_ALL": "C.UTF-8", "LANGUAGE": "",
							"FAULTLINE_TEST_SEED": "witness-live-oracle", "FAULTLINE_NO_BROWSER": "1",
							"FAULTLINE_TEST_OPENAI_URL": side.provider.url,
						}
						for _, name := range []string{"SystemRoot", "SYSTEMROOT", "PATHEXT", "ComSpec"} {
							if value, ok := os.LookupEnv(name); ok {
								want[name] = value
							}
						}
						if !mode.noKey {
							want["OPENAI_API_KEY"] = "fl-test-key"
						}
						if kind == "go" && os.Getenv("GOCOVERDIR") != "" {
							want["GOCOVERDIR"] = os.Getenv("GOCOVERDIR")
						}
						seen := make(map[string]bool)
						for _, entry := range side.cmd(nil).Env {
							name, value, ok := strings.Cut(entry, "=")
							seenName := name
							// Fold coverage aliases only; retain the intentional
							// SystemRoot/SYSTEMROOT allowlist pair as distinct names.
							if strings.EqualFold(name, "GOCOVERDIR") {
								seenName = "GOCOVERDIR"
							}
							if !ok || seen[seenName] {
								t.Fatalf("malformed or duplicate environment name %q", name)
							}
							if expected, allowed := want[name]; !allowed || value != expected {
								t.Errorf("environment name %q is unexpected or has the wrong value", name)
							}
							seen[seenName] = true
						}
						for name := range want {
							if !seen[name] {
								t.Errorf("missing environment name %q", name)
							}
						}
					})
				}
			}
		})
	}
}
