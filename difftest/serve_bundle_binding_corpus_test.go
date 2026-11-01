//go:build faultline_test

package difftest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

// Strictness regressions for the supplemental binding corpus loader. Every case
// mutates a fresh COPY of the real captured corpus under a test temp root (the
// committed corpus is only read) and requires loadServeBundleBindingAt to
// reject it; the valid control proves each rejection comes from its mutation.
// These tests execute no CLI.

func copyBindingCorpus(t *testing.T) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "binding")
	err := filepath.WalkDir(serveBundleBindingDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(serveBundleBindingDir, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !d.Type().IsRegular() {
			t.Fatalf("committed binding corpus holds a link or special file: %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func readBinding(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeBinding(t *testing.T, dir, name, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// bindingLinesOf splits a JSONL file into its lines (no trailing newline).
func bindingLinesOf(t *testing.T, dir, name string) []string {
	t.Helper()
	return strings.Split(strings.TrimSuffix(readBinding(t, dir, name), "\n"), "\n")
}

func writeBindingLines(t *testing.T, dir, name string, lines []string) {
	t.Helper()
	writeBinding(t, dir, name, strings.Join(lines, "\n")+"\n")
}

// editBindingInventory decodes inventory.json generically, applies change and
// writes it back (member order is irrelevant to the loader under test).
func editBindingInventory(t *testing.T, dir string, change func(inv map[string]any)) {
	t.Helper()
	var inv map[string]any
	if err := json.Unmarshal([]byte(readBinding(t, dir, "inventory.json")), &inv); err != nil {
		t.Fatal(err)
	}
	change(inv)
	data, err := json.MarshalIndent(inv, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeBinding(t, dir, "inventory.json", string(data)+"\n")
}

func inventoryFiles(inv map[string]any) []any { return inv["files"].([]any) }

func TestServeBundleBindingLoaderRejectsMalformedCorpora(t *testing.T) {
	original := loadServeBundleCorpus(t)
	first := "minimization/" + serveBundleBindingNames[0] + ".json"
	cases := []struct {
		name   string
		mutate func(t *testing.T, dir string)
		reject bool
	}{
		{name: "valid control", mutate: func(t *testing.T, dir string) {}, reject: false},

		// files on disk
		{name: "missing retained record", reject: true, mutate: func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, first)); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "extra file", reject: true, mutate: func(t *testing.T, dir string) { writeBinding(t, dir, "extra.json", "{}\n") }},
		{name: "extra empty directory", reject: true, mutate: func(t *testing.T, dir string) {
			if err := os.Mkdir(filepath.Join(dir, "empty"), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "extra empty directory below minimization", reject: true, mutate: func(t *testing.T, dir string) {
			if err := os.Mkdir(filepath.Join(dir, "minimization", "empty"), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "edited retained record (hash and bytes differ)", reject: true, mutate: func(t *testing.T, dir string) {
			writeBinding(t, dir, first, readBinding(t, dir, first)+" ")
		}},
		{name: "edited baseline", reject: true, mutate: func(t *testing.T, dir string) {
			writeBinding(t, dir, "baseline.json", strings.Replace(readBinding(t, dir, "baseline.json"), "22.22.2", "22.22.3", 1))
		}},
		{name: "edited gitattributes", reject: true, mutate: func(t *testing.T, dir string) { writeBinding(t, dir, ".gitattributes", "* text\n") }},

		// inventory content
		{name: "wrong inventory hash", reject: true, mutate: func(t *testing.T, dir string) {
			editBindingInventory(t, dir, func(inv map[string]any) { inventoryFiles(inv)[0].(map[string]any)["sha256"] = strings.Repeat("0", 64) })
		}},
		{name: "wrong inventory bytes", reject: true, mutate: func(t *testing.T, dir string) {
			editBindingInventory(t, dir, func(inv map[string]any) { inventoryFiles(inv)[0].(map[string]any)["bytes"] = 1 })
		}},
		{name: "missing inventory path", reject: true, mutate: func(t *testing.T, dir string) {
			editBindingInventory(t, dir, func(inv map[string]any) { inv["files"] = inventoryFiles(inv)[1:] })
		}},
		{name: "duplicate inventory path", reject: true, mutate: func(t *testing.T, dir string) {
			editBindingInventory(t, dir, func(inv map[string]any) {
				files := inventoryFiles(inv)
				files[1] = files[0]
			})
		}},
		{name: "extra inventory path", reject: true, mutate: func(t *testing.T, dir string) {
			editBindingInventory(t, dir, func(inv map[string]any) {
				inv["files"] = append(inventoryFiles(inv), map[string]any{"path": "baseline.json", "sha256": strings.Repeat("0", 64), "bytes": 0})
			})
		}},
		{name: "inventory path outside the fixed set (correct hash and bytes)", reject: true, mutate: func(t *testing.T, dir string) {
			// Points an entry at another retained file whose hash and size it
			// states correctly: the fixed path set, not the data, must decide.
			editBindingInventory(t, dir, func(inv map[string]any) {
				entry := inventoryFiles(inv)[0].(map[string]any)
				data := readBinding(t, dir, "cases.jsonl")
				entry["path"] = "cases.jsonl"
				entry["bytes"] = len(data)
				entry["sha256"] = sha256Hex(data)
			})
		}},
		{name: "invalid source minimization digest", reject: true, mutate: func(t *testing.T, dir string) {
			editBindingInventory(t, dir, func(inv map[string]any) { inv["source"].(map[string]any)["minimizationDigest"] = "not-a-digest" })
		}},
		{name: "well-formed but foreign source minimization digest", reject: true, mutate: func(t *testing.T, dir string) {
			editBindingInventory(t, dir, func(inv map[string]any) {
				inv["source"].(map[string]any)["minimizationDigest"] = "sha256:" + strings.Repeat("0", 64)
			})
		}},
		{name: "invalid source minimization sha256", reject: true, mutate: func(t *testing.T, dir string) {
			editBindingInventory(t, dir, func(inv map[string]any) { inv["source"].(map[string]any)["minimizationSha256"] = "xyz" })
		}},
		{name: "well-formed but foreign source minimization sha256", reject: true, mutate: func(t *testing.T, dir string) {
			editBindingInventory(t, dir, func(inv map[string]any) {
				inv["source"].(map[string]any)["minimizationSha256"] = strings.Repeat("0", 64)
			})
		}},
		{name: "inventory schema", reject: true, mutate: func(t *testing.T, dir string) {
			editBindingInventory(t, dir, func(inv map[string]any) { inv["schema"] = "faultline.serve-bundle-binding.v2" })
		}},
		{name: "unknown inventory member", reject: true, mutate: func(t *testing.T, dir string) {
			editBindingInventory(t, dir, func(inv map[string]any) { inv["extra"] = true })
		}},

		// cases.jsonl
		{name: "case order swapped", reject: true, mutate: func(t *testing.T, dir string) {
			lines := bindingLinesOf(t, dir, "cases.jsonl")
			lines[0], lines[1] = lines[1], lines[0]
			writeBindingLines(t, dir, "cases.jsonl", lines)
		}},
		{name: "case id duplicated", reject: true, mutate: func(t *testing.T, dir string) {
			lines := bindingLinesOf(t, dir, "cases.jsonl")
			lines[1] = lines[0]
			writeBindingLines(t, dir, "cases.jsonl", lines)
		}},
		{name: "case row missing", reject: true, mutate: func(t *testing.T, dir string) {
			writeBindingLines(t, dir, "cases.jsonl", bindingLinesOf(t, dir, "cases.jsonl")[:4])
		}},
		{name: "case argv flag changed", reject: true, mutate: func(t *testing.T, dir string) {
			lines := bindingLinesOf(t, dir, "cases.jsonl")
			lines[0] = strings.Replace(lines[0], `"--port"`, `"--output"`, 1)
			writeBindingLines(t, dir, "cases.jsonl", lines)
		}},
		{name: "case digest changed", reject: true, mutate: func(t *testing.T, dir string) {
			lines := bindingLinesOf(t, dir, "cases.jsonl")
			lines[0] = replaceDigests(lines[0])
			writeBindingLines(t, dir, "cases.jsonl", lines)
		}},
		{name: "case row with a duplicate member", reject: true, mutate: func(t *testing.T, dir string) {
			lines := bindingLinesOf(t, dir, "cases.jsonl")
			lines[0] = strings.TrimSuffix(lines[0], "}") + `,"id":"binding-` + serveBundleBindingNames[0] + `"}`
			writeBindingLines(t, dir, "cases.jsonl", lines)
		}},
		{name: "case row with trailing data", reject: true, mutate: func(t *testing.T, dir string) {
			lines := bindingLinesOf(t, dir, "cases.jsonl")
			lines[0] += " {}"
			writeBindingLines(t, dir, "cases.jsonl", lines)
		}},
		{name: "case row malformed", reject: true, mutate: func(t *testing.T, dir string) {
			lines := bindingLinesOf(t, dir, "cases.jsonl")
			lines[0] = lines[0][:len(lines[0])-1]
			writeBindingLines(t, dir, "cases.jsonl", lines)
		}},
		{name: "case row with a missing member", reject: true, mutate: func(t *testing.T, dir string) {
			lines := bindingLinesOf(t, dir, "cases.jsonl")
			lines[0] = removeMember(t, lines[0], "input")
			writeBindingLines(t, dir, "cases.jsonl", lines)
		}},
		{name: "blank cases line", reject: true, mutate: func(t *testing.T, dir string) {
			lines := bindingLinesOf(t, dir, "cases.jsonl")
			writeBindingLines(t, dir, "cases.jsonl", append(lines[:2:2], append([]string{""}, lines[2:]...)...))
		}},
		{name: "cases without final newline", reject: true, mutate: func(t *testing.T, dir string) {
			writeBinding(t, dir, "cases.jsonl", strings.TrimSuffix(readBinding(t, dir, "cases.jsonl"), "\n"))
		}},

		// golden.jsonl
		{name: "golden order swapped", reject: true, mutate: func(t *testing.T, dir string) {
			lines := bindingLinesOf(t, dir, "golden.jsonl")
			lines[0], lines[1] = lines[1], lines[0]
			writeBindingLines(t, dir, "golden.jsonl", lines)
		}},
		{name: "golden row missing", reject: true, mutate: func(t *testing.T, dir string) {
			writeBindingLines(t, dir, "golden.jsonl", bindingLinesOf(t, dir, "golden.jsonl")[:4])
		}},
		{name: "golden row malformed", reject: true, mutate: func(t *testing.T, dir string) {
			lines := bindingLinesOf(t, dir, "golden.jsonl")
			lines[0] = lines[0][:len(lines[0])-2]
			writeBindingLines(t, dir, "golden.jsonl", lines)
		}},
		{name: "golden row with trailing data", reject: true, mutate: func(t *testing.T, dir string) {
			lines := bindingLinesOf(t, dir, "golden.jsonl")
			lines[0] += " 1"
			writeBindingLines(t, dir, "golden.jsonl", lines)
		}},
		{name: "golden with a missing signal member", reject: true, mutate: func(t *testing.T, dir string) {
			lines := bindingLinesOf(t, dir, "golden.jsonl")
			lines[0] = removeProcessMember(t, lines[0], "signal")
			writeBindingLines(t, dir, "golden.jsonl", lines)
		}},
		{name: "golden with a missing stdout member", reject: true, mutate: func(t *testing.T, dir string) {
			lines := bindingLinesOf(t, dir, "golden.jsonl")
			lines[0] = removeProcessMember(t, lines[0], "stdout")
			writeBindingLines(t, dir, "golden.jsonl", lines)
		}},
		{name: "golden with a missing exitCode member", reject: true, mutate: func(t *testing.T, dir string) {
			lines := bindingLinesOf(t, dir, "golden.jsonl")
			lines[0] = removeProcessMember(t, lines[0], "exitCode")
			writeBindingLines(t, dir, "golden.jsonl", lines)
		}},
		{name: "golden with a duplicate member of equal value", reject: true, mutate: func(t *testing.T, dir string) {
			lines := bindingLinesOf(t, dir, "golden.jsonl")
			lines[0] = strings.Replace(lines[0], `"signal":null`, `"signal":null,"signal":null`, 1)
			writeBindingLines(t, dir, "golden.jsonl", lines)
		}},
		{name: "golden with a duplicate id", reject: true, mutate: func(t *testing.T, dir string) {
			lines := bindingLinesOf(t, dir, "golden.jsonl")
			lines[0] = strings.Replace(lines[0], `{"id":`, `{"id":"x","id":`, 1)
			writeBindingLines(t, dir, "golden.jsonl", lines)
		}},
		// Exact member types: JSON null is not an empty string, and a non-string
		// is not a string. encoding/json would decode null into "" silently.
		{name: "golden with stdout null", reject: true, mutate: func(t *testing.T, dir string) {
			lines := bindingLinesOf(t, dir, "golden.jsonl")
			if !strings.Contains(lines[0], `"stdout":""`) {
				t.Fatalf("first golden has no empty stdout member to replace: %s", lines[0])
			}
			lines[0] = strings.Replace(lines[0], `"stdout":""`, `"stdout":null`, 1)
			writeBindingLines(t, dir, "golden.jsonl", lines)
		}},
		{name: "golden with stdout of a non-string type", reject: true, mutate: func(t *testing.T, dir string) {
			lines := bindingLinesOf(t, dir, "golden.jsonl")
			lines[0] = strings.Replace(lines[0], `"stdout":""`, `"stdout":0`, 1)
			writeBindingLines(t, dir, "golden.jsonl", lines)
		}},
		{name: "golden exit zero", reject: true, mutate: func(t *testing.T, dir string) {
			lines := bindingLinesOf(t, dir, "golden.jsonl")
			lines[0] = strings.Replace(lines[0], `"exitCode":1`, `"exitCode":0`, 1)
			writeBindingLines(t, dir, "golden.jsonl", lines)
		}},
		{name: "golden with a signal", reject: true, mutate: func(t *testing.T, dir string) {
			lines := bindingLinesOf(t, dir, "golden.jsonl")
			lines[0] = strings.Replace(lines[0], `"signal":null`, `"signal":"SIGKILL"`, 1)
			writeBindingLines(t, dir, "golden.jsonl", lines)
		}},
		{name: "golden with stdout", reject: true, mutate: func(t *testing.T, dir string) {
			lines := bindingLinesOf(t, dir, "golden.jsonl")
			lines[0] = strings.Replace(lines[0], `"stdout":""`, `"stdout":"x"`, 1)
			writeBindingLines(t, dir, "golden.jsonl", lines)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := copyBindingCorpus(t)
			c.mutate(t, dir)
			b, err := loadServeBundleBindingAt(dir, original)
			switch {
			case c.reject && err == nil:
				t.Fatalf("loader accepted the corpus (%d cases)", len(b.cases))
			case !c.reject && err != nil:
				t.Fatalf("loader rejected the unmodified corpus copy: %v", err)
			}
		})
	}
}

// Catches a loader that trusts a link: the retained record or a metadata file
// replaced by a symbolic link must be rejected. Only the Windows privilege error
// (ERROR_PRIVILEGE_NOT_HELD, 1314) skips; any other failure fails.
func TestServeBundleBindingLoaderRejectsLinks(t *testing.T) {
	original := loadServeBundleCorpus(t)
	for _, name := range []string{"minimization/" + serveBundleBindingNames[0] + ".json", "cases.jsonl", "golden.jsonl", "inventory.json", "baseline.json", ".gitattributes"} {
		t.Run(name, func(t *testing.T) {
			dir := copyBindingCorpus(t)
			target := filepath.Join(dir, filepath.FromSlash(name))
			data := readBinding(t, dir, name)
			elsewhere := filepath.Join(t.TempDir(), "elsewhere")
			if err := os.WriteFile(elsewhere, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(elsewhere, target); err != nil {
				var errno syscall.Errno
				if runtime.GOOS == "windows" && errors.As(err, &errno) && errno == 1314 {
					t.Skip("symlink creation needs a privilege this Windows host lacks (ERROR_PRIVILEGE_NOT_HELD): " + err.Error())
				}
				t.Fatal(err)
			}
			if _, err := loadServeBundleBindingAt(dir, original); err == nil {
				t.Fatal("loader accepted a symbolic link")
			}
		})
	}
}

// Catches a copier that rereads the retained record without checking it against
// the inventory it was loaded under: a record changed after load must be
// refused before any process could use it.
func TestServeBundleBindingCopyRejectsRecordChangedAfterLoad(t *testing.T) {
	original := loadServeBundleCorpus(t)
	dir := copyBindingCorpus(t)
	loaded, err := loadServeBundleBindingAt(dir, original)
	if err != nil {
		t.Fatal(err)
	}
	c := loaded.cases[0]
	writeBinding(t, dir, c.Input, readBinding(t, dir, c.Input)+" ")
	root := filepath.Join(t.TempDir(), "fixture")
	if err := copyBindingCaseAt(original, loaded, c, root); err == nil {
		data, _ := os.ReadFile(filepath.Join(root, serveBundleBindingInput))
		t.Fatalf("copier used a record that no longer matches its inventory (%d bytes)", len(data))
	}
	if _, err := os.Stat(filepath.Join(root, serveBundleBindingInput)); err == nil {
		t.Fatal("the changed record was left in the case root")
	}
}

func sha256Hex(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func replaceDigests(line string) string {
	const marker = `"expectedMinimizationDigest":"sha256:`
	i := strings.Index(line, marker)
	if i < 0 {
		return line
	}
	start := i + len(marker)
	return line[:start] + strings.Repeat("0", 64) + line[start+64:]
}

func removeMember(t *testing.T, line, key string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatal(err)
	}
	delete(m, key)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func removeProcessMember(t *testing.T, line, key string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatal(err)
	}
	var process map[string]json.RawMessage
	if err := json.Unmarshal(m["process"], &process); err != nil {
		t.Fatal(err)
	}
	delete(process, key)
	raw, err := json.Marshal(process)
	if err != nil {
		t.Fatal(err)
	}
	m["process"] = raw
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
