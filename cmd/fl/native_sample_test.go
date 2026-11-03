package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Mizore66/faultline/internal/nativefixture"
)

func TestNativeSampleDispatch(t *testing.T) {
	originalArgs, originalOut, originalErr := os.Args, os.Stdout, os.Stderr
	t.Cleanup(func() { os.Args, os.Stdout, os.Stderr = originalArgs, originalOut, originalErr })
	for _, tc := range []struct {
		name, marker string
		args         []string
	}{
		{"ordinary-help", "", []string{"--help"}},
		{"arg-without-marker", "", []string{nativefixture.ChildArg}},
		{"wrong-marker", "other", []string{nativefixture.ChildArg}},
		{"marker-without-arg", "v1", nil},
		{"marker-with-help", "v1", []string{"--help"}},
		{"extra-arg", "v1", []string{nativefixture.ChildArg, "extra"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(nativefixture.ChildEnvName, tc.marker)
			os.Args = append([]string{"fl"}, tc.args...)
			if code, handled := runNativeSample(); handled || code != 0 {
				t.Fatalf("ordinary dispatch intercepted: code=%d handled=%v", code, handled)
			}
		})
	}
}

func TestNativeSampleExactProtocol(t *testing.T) {
	originalArgs, originalOut, originalErr := os.Args, os.Stdout, os.Stderr
	t.Cleanup(func() { os.Args, os.Stdout, os.Stderr = originalArgs, originalOut, originalErr })
	for _, tc := range []struct {
		name, config, stdout, stderr string
		code                         int
	}{
		{"base", "{\"displayDefault\":\"ORDER_CURRENCY\",\"settlementSource\":\"ORDER_CURRENCY\"}\n", "settlement currency preserved\n{\"protocol\":\"faultline.witness-result.v1\",\"outcome\":\"PREDICATE_PASS\"}\n", "", 0},
		{"source", "{\"displayDefault\":\"EUR\",\"settlementSource\":\"ORDER_CURRENCY\"}\n", "settlement currency preserved\n{\"protocol\":\"faultline.witness-result.v1\",\"outcome\":\"PREDICATE_PASS\"}\n", "", 0},
		{"renderer", "{\"displayDefault\":\"ORDER_CURRENCY\",\"settlementSource\":\"DISPLAY_CURRENCY\"}\n", "settlement currency preserved\n{\"protocol\":\"faultline.witness-result.v1\",\"outcome\":\"PREDICATE_PASS\"}\n", "", 0},
		{"both", "{\"displayDefault\":\"EUR\",\"settlementSource\":\"DISPLAY_CURRENCY\"}\n", "{\"protocol\":\"faultline.witness-result.v1\",\"outcome\":\"PREDICATE_FAIL\"}\n", "expected settlement USD; received EUR\n", 1},
		{"partial", "{\"displayDefault\":\"ORDER_CURRENCY\",\"settlementSource\":}\n", "", "native fixture config: missing value after object key\n", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			if err := os.WriteFile(filepath.Join(dir, nativefixture.ConfigFile), []byte(tc.config), 0600); err != nil {
				t.Fatal(err)
			}
			out, err := os.CreateTemp(dir, "stdout-")
			if err != nil {
				t.Fatal(err)
			}
			defer out.Close()
			stderr, err := os.CreateTemp(dir, "stderr-")
			if err != nil {
				t.Fatal(err)
			}
			defer stderr.Close()
			os.Args, os.Stdout, os.Stderr = []string{"fl", nativefixture.ChildArg}, out, stderr
			t.Setenv(nativefixture.ChildEnvName, nativefixture.ChildEnvValue)
			code, handled := runNativeSample()
			os.Stdout, os.Stderr = originalOut, originalErr
			stdoutBytes, err := os.ReadFile(out.Name())
			if err != nil {
				t.Fatal(err)
			}
			stderrBytes, err := os.ReadFile(stderr.Name())
			if err != nil {
				t.Fatal(err)
			}
			if !handled || code != tc.code || string(stdoutBytes) != tc.stdout || string(stderrBytes) != tc.stderr {
				t.Fatalf("code=%d handled=%v stdout=%q stderr=%q", code, handled, stdoutBytes, stderrBytes)
			}
		})
	}
}
