//go:build faultline_test

package difftest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Integrity regressions for the native evidence audit. Every case mutates a
// private temp copy of the synthetic evidence (the validator's own fixtures, not
// native evidence) and requires the real auditServeBundleEvidence to reject it;
// the valid control runs first so each rejection comes from its mutation. These
// tests name behavior the audit must have; they change no existing expectation.

func integrityRecordPath(dir, side string) string {
	return filepath.Join(dir, "captures", evidenceOriginalCorpus, "cli-error-x", side, "record.json")
}

func integrityEditJSON(t *testing.T, path string, change func(m map[string]any)) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	change(m)
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func integrityEditText(t *testing.T, path string, change func(text string) string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := change(string(data))
	if edited == string(data) {
		t.Fatalf("the text edit of %s changed nothing", path)
	}
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
}

func integrityInventoryFiles(m map[string]any) []any { return m["files"].([]any) }

func TestServeBundleEvidenceIntegrity(t *testing.T) {
	const foreign = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	cases := []struct {
		name   string
		mutate func(t *testing.T, dir string)
		reject bool
	}{
		{name: "valid control", reject: false, mutate: func(t *testing.T, dir string) {}},

		// inventory.json
		{name: "inventory entry sha256 edited", reject: true, mutate: func(t *testing.T, dir string) {
			integrityEditJSON(t, filepath.Join(dir, "inventory.json"), func(m map[string]any) {
				integrityInventoryFiles(m)[0].(map[string]any)["sha256"] = strings.Repeat("0", 64)
			})
		}},
		{name: "inventory entry bytes edited", reject: true, mutate: func(t *testing.T, dir string) {
			integrityEditJSON(t, filepath.Join(dir, "inventory.json"), func(m map[string]any) {
				integrityInventoryFiles(m)[0].(map[string]any)["bytes"] = 999
			})
		}},
		{name: "inventory entry removed (golden of a corpus)", reject: true, mutate: func(t *testing.T, dir string) {
			integrityEditJSON(t, filepath.Join(dir, "inventory.json"), func(m map[string]any) {
				var kept []any
				for _, f := range integrityInventoryFiles(m) {
					if f.(map[string]any)["path"] != "serve-bundle/golden.jsonl" {
						kept = append(kept, f)
					}
				}
				m["files"] = kept
			})
		}},
		{name: "inventory has an extra entry", reject: true, mutate: func(t *testing.T, dir string) {
			integrityEditJSON(t, filepath.Join(dir, "inventory.json"), func(m map[string]any) {
				m["files"] = append(integrityInventoryFiles(m), map[string]any{"path": "serve-bundle/not-a-corpus-file", "sha256": strings.Repeat("a", 64), "bytes": 1})
			})
		}},
		{name: "inventory has a duplicate entry", reject: true, mutate: func(t *testing.T, dir string) {
			integrityEditJSON(t, filepath.Join(dir, "inventory.json"), func(m map[string]any) {
				files := integrityInventoryFiles(m)
				m["files"] = append(files, files[0])
			})
		}},
		{name: "inventory with a duplicate JSON member", reject: true, mutate: func(t *testing.T, dir string) {
			integrityEditText(t, filepath.Join(dir, "inventory.json"), func(text string) string {
				return strings.Replace(text, `{"schema":`, `{"schema":"`+serveBundleEvidenceSchema+`","schema":`, 1)
			})
		}},

		// record.json provenance fields
		{name: "record fixture altered", reject: true, mutate: func(t *testing.T, dir string) {
			integrityEditJSON(t, integrityRecordPath(dir, "ts"), func(m map[string]any) { m["fixture"] = "another" })
		}},
		{name: "record expectedRoot altered", reject: true, mutate: func(t *testing.T, dir string) {
			integrityEditJSON(t, integrityRecordPath(dir, "ts"), func(m map[string]any) { m["expectedRoot"] = foreign })
		}},
		{name: "record digests altered", reject: true, mutate: func(t *testing.T, dir string) {
			integrityEditJSON(t, integrityRecordPath(dir, "go"), func(m map[string]any) { m["digests"] = map[string]any{"minimization": foreign} })
		}},
		{name: "record input altered", reject: true, mutate: func(t *testing.T, dir string) {
			integrityEditJSON(t, integrityRecordPath(dir, "ts"), func(m map[string]any) { m["input"] = "minimization/other.json" })
		}},

		// null and missing members
		{name: "record input is null", reject: true, mutate: func(t *testing.T, dir string) {
			integrityEditJSON(t, integrityRecordPath(dir, "ts"), func(m map[string]any) { m["input"] = nil })
		}},
		{name: "record input is missing", reject: true, mutate: func(t *testing.T, dir string) {
			integrityEditJSON(t, integrityRecordPath(dir, "ts"), func(m map[string]any) { delete(m, "input") })
		}},
		{name: "record fixture is missing", reject: true, mutate: func(t *testing.T, dir string) {
			integrityEditJSON(t, integrityRecordPath(dir, "go"), func(m map[string]any) { delete(m, "fixture") })
		}},
		{name: "record normalizedStdout is null", reject: true, mutate: func(t *testing.T, dir string) {
			integrityEditJSON(t, integrityRecordPath(dir, "ts"), func(m map[string]any) { m["normalizedStdout"] = nil })
		}},
		{name: "record digests is missing", reject: true, mutate: func(t *testing.T, dir string) {
			integrityEditJSON(t, integrityRecordPath(dir, "ts"), func(m map[string]any) { delete(m, "digests") })
		}},

		// duplicate JSON members
		{name: "record with a duplicate JSON member", reject: true, mutate: func(t *testing.T, dir string) {
			integrityEditText(t, integrityRecordPath(dir, "ts"), func(text string) string {
				return strings.Replace(text, "{\n", "{\n  \"id\": \"cli-error-x\",\n", 1)
			})
		}},
		{name: "identity with a duplicate JSON member", reject: true, mutate: func(t *testing.T, dir string) {
			integrityEditText(t, filepath.Join(dir, "identity.json"), func(text string) string {
				return strings.Replace(text, `{"schema":`, `{"schema":"`+serveBundleEvidenceSchema+`","schema":`, 1)
			})
		}},

		// raw bytes are the evidence; normalized text must follow from them
		{name: "raw stderr and its record hash and size changed together, normalized unchanged", reject: true, mutate: func(t *testing.T, dir string) {
			const replaced = "tampered\n"
			if err := os.WriteFile(filepath.Join(filepath.Dir(integrityRecordPath(dir, "ts")), "stderr"), []byte(replaced), 0o600); err != nil {
				t.Fatal(err)
			}
			integrityEditJSON(t, integrityRecordPath(dir, "ts"), func(m map[string]any) {
				m["stderrSha256"] = sha256Of([]byte(replaced))
				m["stderrBytes"] = len(replaced)
			})
		}},
		{name: "raw stdout and its record hash and size changed together, normalized unchanged", reject: true, mutate: func(t *testing.T, dir string) {
			const replaced = "unexpected output\n"
			if err := os.WriteFile(filepath.Join(filepath.Dir(integrityRecordPath(dir, "go")), "stdout"), []byte(replaced), 0o600); err != nil {
				t.Fatal(err)
			}
			integrityEditJSON(t, integrityRecordPath(dir, "go"), func(m map[string]any) {
				m["stdoutSha256"] = sha256Of([]byte(replaced))
				m["stdoutBytes"] = len(replaced)
			})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, want := syntheticEvidence(t)
			c.mutate(t, dir)
			err := auditServeBundleEvidence(dir, want)
			switch {
			case c.reject && err == nil:
				t.Fatal("audit accepted the mutated evidence")
			case !c.reject && err != nil:
				t.Fatalf("audit rejected the valid synthetic evidence: %v", err)
			}
		})
	}
}

// Catches a strict decoder that tolerates a repeated member: encoding/json keeps
// the last value, so a duplicate would otherwise pass unseen.
func TestServeBundleEvidenceStrictDecodeRejectsDuplicates(t *testing.T) {
	write := func(text string) string {
		path := filepath.Join(t.TempDir(), "r.json")
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	var good struct {
		A string `json:"a"`
		B struct {
			C string `json:"c"`
		} `json:"b"`
	}
	if err := strictEvidenceDecode(write(`{"a":"x","b":{"c":"y"}}`), &good); err != nil || good.A != "x" || good.B.C != "y" {
		t.Fatalf("valid control: %v %+v", err, good)
	}
	for name, text := range map[string]string{
		"duplicate top-level member (equal value)": `{"a":"x","a":"x","b":{"c":"y"}}`,
		"duplicate top-level member (last wins)":   `{"a":"bad","a":"x","b":{"c":"y"}}`,
		"duplicate nested member":                  `{"a":"x","b":{"c":"y","c":"y"}}`,
		"null for a string member":                 `{"a":null,"b":{"c":"y"}}`,
		"missing member":                           `{"a":"x"}`,
	} {
		var got struct {
			A string `json:"a"`
			B struct {
				C string `json:"c"`
			} `json:"b"`
		}
		if err := strictEvidenceDecode(write(text), &got); err == nil {
			t.Errorf("%s: accepted (decoded %+v)", name, got)
		}
	}
}
