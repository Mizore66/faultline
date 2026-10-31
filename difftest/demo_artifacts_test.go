//go:build faultline_test

package difftest

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/demoartifactstest"
	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// Permanent replay of the Slice 5b.1 cached-artifact corpus
// (difftest/testdata/demo-artifacts). It needs no Node, Git or history: the
// built Go library and the real tagged fl binary are compared with committed
// frozen-TS captures.

const (
	demoArtifactsDir     = "testdata/demo-artifacts"
	demoArtifactsMaxLine = 32 << 20
	demoTmp              = "<TMP>"
)

// demoArtifactMatrix is Task 1's fixed request matrix, by ID and kind, in
// capture order. It is written by hand from the generator's request table so
// a loader cannot satisfy it by echoing the file it loads.
var demoArtifactMatrix = []struct{ id, kind string }{
	{"analysis-replay", "analysis"},
	{"render-default", "render"},
	{"render-replay", "render"},
	{"render-rerun-refused", "render"},
	{"render-no-boundary", "render"},
	{"render-escape-fallback", "render"},
	{"writer-replay", "writer"},
	{"writer-rerun", "writer"},
	{"writer-unicode", "writer"},
	{"writer-lone-surrogate", "writer"},
	{"writer-extra-keys", "writer"},
	{"writer-replace", "writer"},
	{"writer-custom-root", "writer"},
	{"writer-empty-root", "writer"},
	{"writer-missing-catalog", "writer"},
	{"writer-duplicate-id", "writer"},
	{"writer-sanitized-collision", "writer"},
	{"writer-root", "writer"},
	{"writer-outside", "writer"},
	{"writer-parent-file", "writer"},
	{"writer-output-file", "writer"},
	{"writer-invalid-existing", "writer"},
	{"writer-linked-root", "writer"},
	{"writer-linked-output", "writer"},
	{"writer-linked-bundle-file", "writer"},
	{"cli-default", "cli"},
	{"cli-custom", "cli"},
	{"cli-absolute", "cli"},
	{"cli-upper-ext", "cli"},
	{"cli-hidden-ext", "cli"},
	{"cli-double-dot-ext", "cli"},
	{"cli-trailing-output", "cli"},
	{"cli-ignored", "cli"},
	{"cli-equals", "cli"},
	{"cli-duplicate", "cli"},
	{"cli-overwrite", "cli"},
	{"cli-empty-output", "cli"},
	{"cli-next-flag", "cli"},
	{"cli-extension", "cli"},
	{"cli-dotfile-no-ext", "cli"},
	{"cli-parent-file", "cli"},
	{"cli-output-directory", "cli"},
	{"cli-linked-parent", "cli"},
	{"cli-linked-output", "cli"},
	{"cli-dangling-output", "cli"},
}

// demoWindowsLinkCases are the only replay cases a Windows runner without the
// symlink privilege may skip (named in .github/workflows/go.yml).
var demoWindowsLinkCases = map[string]bool{
	"writer-linked-root": true, "writer-linked-output": true, "writer-linked-bundle-file": true,
	"cli-linked-parent": true, "cli-linked-output": true, "cli-dangling-output": true,
}

// demoWantBaseline is the six declared metadata fields, verbatim.
var demoWantBaseline = map[string]string{
	"schema":               "faultline.demo-artifacts-oracle.v1",
	"sourceCommit":         "ce99a793af0b020bf1b9cf21114f8d7897b37207",
	"manifestCommit":       "647440884a6668042f7726f2cbade38886d00a41",
	"manifestVersion":      "0.1.4",
	"nodeVersion":          "22.22.2",
	"cachedRuntimeVersion": "v22.22.2",
}

// validateDemoArtifactsBaseline requires exactly the six fields, all strings
// with the declared values. encoding/json decodes this control metadata only.
func validateDemoArtifactsBaseline(raw []byte) error {
	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		return err
	}
	// Unmarshal keeps the last of duplicate members; count the member names in
	// the token stream so a duplicate cannot hide a corrupted value.
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return fmt.Errorf("demo-artifacts metadata is not an object: %v", err)
	}
	seen := map[string]bool{}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("demo-artifacts metadata member name %v", keyTok)
		}
		if seen[key] {
			return fmt.Errorf("demo-artifacts metadata member %q appears more than once", key)
		}
		seen[key] = true
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return err
		}
	}
	if len(got) != len(demoWantBaseline) {
		return fmt.Errorf("demo-artifacts metadata fields: got %d, want %d", len(got), len(demoWantBaseline))
	}
	for k, want := range demoWantBaseline {
		if v, ok := got[k]; !ok || v != want {
			return fmt.Errorf("demo-artifacts metadata %s: got %q, want %q", k, v, want)
		}
	}
	return nil
}

// demoCaptureKeys is the exact key set of one golden record.
var demoCaptureKeys = []string{"after", "before", "call", "process", "request", "schema"}

func demoObjectKeys(v jsjson.Value, what string, want []string) error {
	if v.Kind() != jsjson.Object {
		return fmt.Errorf("%s is not an object", what)
	}
	got := v.Obj().Keys()
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		return fmt.Errorf("%s keys %v, want %v", what, got, want)
	}
	return nil
}

func demoString(v jsjson.Value, what string) (string, error) {
	if v.Kind() != jsjson.String {
		return "", fmt.Errorf("%s is not a string", what)
	}
	return v.Str(), nil
}

func demoNullableString(v jsjson.Value, what string) (*string, error) {
	switch v.Kind() {
	case jsjson.Null:
		return nil, nil
	case jsjson.String:
		s := v.Str()
		return &s, nil
	}
	return nil, fmt.Errorf("%s is neither a string nor null", what)
}

func demoRequestFromValue(v jsjson.Value) (demoartifactstest.Request, error) {
	var r demoartifactstest.Request
	if v.Kind() != jsjson.Object {
		return r, errors.New("request is not an object")
	}
	allowed := map[string]bool{"id": true, "kind": true, "base": true, "mutation": true, "setup": true, "output": true, "proofRoot": true, "argv": true}
	for _, k := range v.Obj().Keys() {
		if !allowed[k] {
			return r, fmt.Errorf("request has unknown field %q", k)
		}
	}
	var err error
	for _, f := range []struct {
		key string
		dst *string
	}{{"id", &r.ID}, {"kind", &r.Kind}, {"base", &r.Base}, {"mutation", &r.Mutation}, {"setup", &r.Setup}} {
		member := v.Get(f.key)
		if member.Kind() == jsjson.Undefined {
			continue
		}
		if *f.dst, err = demoString(member, "request."+f.key); err != nil {
			return r, err
		}
	}
	for _, f := range []struct {
		key string
		dst **string
	}{{"output", &r.Output}, {"proofRoot", &r.ProofRoot}} {
		member := v.Get(f.key)
		if member.Kind() == jsjson.Undefined {
			continue
		}
		s, err := demoString(member, "request."+f.key)
		if err != nil {
			return r, err
		}
		*f.dst = &s
	}
	if argv := v.Get("argv"); argv.Kind() != jsjson.Undefined {
		if argv.Kind() != jsjson.Array {
			return r, errors.New("request.argv is not an array")
		}
		items := []string{}
		for i, item := range argv.Items() {
			s, err := demoString(item, fmt.Sprintf("request.argv[%d]", i))
			if err != nil {
				return r, err
			}
			items = append(items, s)
		}
		r.Argv = &items
	}
	return r, nil
}

func demoInventoryFromValue(v jsjson.Value, what string) ([]demoartifactstest.Entry, error) {
	if v.Kind() != jsjson.Array {
		return nil, fmt.Errorf("%s is not an array", what)
	}
	out := []demoartifactstest.Entry{}
	for i, item := range v.Items() {
		if item.Kind() != jsjson.Object {
			return nil, fmt.Errorf("%s[%d] is not an object", what, i)
		}
		var e demoartifactstest.Entry
		var err error
		for _, k := range item.Obj().Keys() {
			if k != "path" && k != "kind" && k != "base64" && k != "target" {
				return nil, fmt.Errorf("%s[%d] has unknown field %q", what, i, k)
			}
		}
		if e.Path, err = demoString(item.Get("path"), fmt.Sprintf("%s[%d].path", what, i)); err != nil {
			return nil, err
		}
		if e.Kind, err = demoString(item.Get("kind"), fmt.Sprintf("%s[%d].kind", what, i)); err != nil {
			return nil, err
		}
		for _, f := range []struct {
			key string
			dst **string
		}{{"base64", &e.Base64}, {"target", &e.Target}} {
			member := item.Get(f.key)
			if member.Kind() == jsjson.Undefined {
				continue
			}
			s, err := demoString(member, fmt.Sprintf("%s[%d].%s", what, i, f.key))
			if err != nil {
				return nil, err
			}
			*f.dst = &s
		}
		out = append(out, e)
	}
	return out, nil
}

// demoCaptureFromValue converts one parsed capture. The schema member is
// required when withSchema is set (a committed record) and absent otherwise
// (the live adapter's raw output, to which the generator adds it).
func demoCaptureFromValue(v jsjson.Value, withSchema bool) (demoartifactstest.Capture, error) {
	var c demoartifactstest.Capture
	keys := demoCaptureKeys
	if !withSchema {
		keys = []string{"after", "before", "call", "process", "request"}
	}
	if err := demoObjectKeys(v, "capture", keys); err != nil {
		return c, err
	}
	var err error
	if withSchema {
		if c.Schema, err = demoString(v.Get("schema"), "schema"); err != nil {
			return c, err
		}
	} else {
		c.Schema = demoartifactstest.CaptureSchema
	}
	if c.Request, err = demoRequestFromValue(v.Get("request")); err != nil {
		return c, err
	}
	if call := v.Get("call"); call.Kind() != jsjson.Null {
		if err := demoObjectKeys(call, "call", []string{"error", "valueJSON"}); err != nil {
			return c, err
		}
		c.Call = &demoartifactstest.Call{}
		if c.Call.ValueJSON, err = demoNullableString(call.Get("valueJSON"), "call.valueJSON"); err != nil {
			return c, err
		}
		if c.Call.Error, err = demoNullableString(call.Get("error"), "call.error"); err != nil {
			return c, err
		}
	}
	if p := v.Get("process"); p.Kind() != jsjson.Null {
		if err := demoObjectKeys(p, "process", []string{"exitCode", "stderr", "stdout"}); err != nil {
			return c, err
		}
		c.Process = &demoartifactstest.Process{}
		if c.Process.Stdout, err = demoString(p.Get("stdout"), "process.stdout"); err != nil {
			return c, err
		}
		if c.Process.Stderr, err = demoString(p.Get("stderr"), "process.stderr"); err != nil {
			return c, err
		}
		code := p.Get("exitCode")
		if code.Kind() != jsjson.Number || code.Num() != float64(int(code.Num())) || code.Num() < 0 {
			return c, errors.New("process.exitCode is not a non-negative integer")
		}
		c.Process.ExitCode = int(code.Num())
	}
	if c.Before, err = demoInventoryFromValue(v.Get("before"), "before"); err != nil {
		return c, err
	}
	if c.After, err = demoInventoryFromValue(v.Get("after"), "after"); err != nil {
		return c, err
	}
	return c, nil
}

// demoValidateCases applies the committed-record rules to the converted
// captures: schema, the fixed ID matrix, the process/call discriminator and
// inventory shape.
func demoValidateCases(cases []demoartifactstest.Capture) error {
	if len(cases) != len(demoArtifactMatrix) {
		return fmt.Errorf("%d cases, want the fixed %d", len(cases), len(demoArtifactMatrix))
	}
	for i, c := range cases {
		want := demoArtifactMatrix[i]
		if c.Request.ID != want.id {
			return fmt.Errorf("case %d is %q, want %q (duplicate, missing, unknown or reordered ID)", i+1, c.Request.ID, want.id)
		}
		if c.Request.Kind != want.kind {
			return fmt.Errorf("%s: kind %q, want %q", want.id, c.Request.Kind, want.kind)
		}
		if c.Schema != demoartifactstest.CaptureSchema {
			return fmt.Errorf("%s: schema %q", want.id, c.Schema)
		}
		if want.kind == "cli" {
			if c.Call != nil || c.Process == nil {
				return fmt.Errorf("%s: a CLI case needs a process and a null call", want.id)
			}
			if c.Process.Stdout == "" && c.Process.Stderr == "" {
				return fmt.Errorf("%s: empty process capture", want.id)
			}
		} else {
			if c.Process != nil || c.Call == nil {
				return fmt.Errorf("%s: a library case needs a call and a null process", want.id)
			}
			if (c.Call.ValueJSON == nil) == (c.Call.Error == nil) {
				return fmt.Errorf("%s: exactly one of value and error is required", want.id)
			}
			if (c.Call.ValueJSON != nil && *c.Call.ValueJSON == "") || (c.Call.Error != nil && *c.Call.Error == "") {
				return fmt.Errorf("%s: empty call capture", want.id)
			}
		}
		for _, inv := range []struct {
			name string
			list []demoartifactstest.Entry
		}{{"before", c.Before}, {"after", c.After}} {
			if err := demoValidateInventory(inv.list); err != nil {
				return fmt.Errorf("%s %s: %v", want.id, inv.name, err)
			}
		}
	}
	return nil
}

// demoValidateInventory requires byte-sorted unique paths and, per kind, exactly
// the members that kind carries: canonical base64 for a file, a nonempty
// relative target for a link, neither for a directory.
func demoValidateInventory(list []demoartifactstest.Entry) error {
	for i, e := range list {
		if i > 0 && list[i-1].Path >= e.Path {
			return fmt.Errorf("paths not byte-sorted and unique at %q", e.Path)
		}
		switch e.Kind {
		case "directory":
			if e.Base64 != nil || e.Target != nil {
				return fmt.Errorf("directory %q carries file or link members", e.Path)
			}
		case "file":
			if e.Base64 == nil || e.Target != nil {
				return fmt.Errorf("file %q needs base64 and no target", e.Path)
			}
			raw, err := base64.StdEncoding.DecodeString(*e.Base64)
			if err != nil || base64.StdEncoding.EncodeToString(raw) != *e.Base64 {
				return fmt.Errorf("file %q has non-canonical base64", e.Path)
			}
		case "symlink":
			if e.Target == nil || e.Base64 != nil || *e.Target == "" || strings.HasPrefix(*e.Target, "/") {
				return fmt.Errorf("link %q needs a nonempty relative target and no base64", e.Path)
			}
		default:
			return fmt.Errorf("entry %q has kind %q", e.Path, e.Kind)
		}
	}
	return nil
}

// demoLFSplit splits on LF only: bufio.ScanLines would also drop a trailing CR
// and so hide a corrupted line ending. Data after the last LF is an error.
func demoLFSplit(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return 0, nil, errors.New("golden.jsonl lacks a final newline")
	}
	return 0, nil, nil
}

// scanDemoArtifactLines splits the corpus into its LF-terminated lines under a
// per-line byte limit. A longer line is bufio.ErrTooLong, never truncated.
func scanDemoArtifactLines(golden []byte, maxLine int) ([]string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(golden))
	// bufio allows a token up to the larger of the initial capacity and
	// maxLine, so the initial buffer must not exceed the configured limit.
	scanner.Buffer(make([]byte, 0, min(64*1024, maxLine)), maxLine)
	scanner.Split(demoLFSplit)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

// parseDemoArtifactCases strictly loads the golden JSONL. Each line is parsed
// by jsjson (so WTF-8 text is preserved), must re-serialize to itself byte for
// byte (the generator writes canonical JSON.stringify output), and is bounded
// by maxLine; an oversized line is an error, never truncated.
func parseDemoArtifactCases(golden []byte, maxLine int) ([]demoartifactstest.Capture, error) {
	lines, err := scanDemoArtifactLines(golden, maxLine)
	if err != nil {
		return nil, err
	}
	var cases []demoartifactstest.Capture
	for i, line := range lines {
		n := i + 1
		v, err := jsjson.Parse(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", n, err)
		}
		if jsjson.Stringify(v) != line {
			return nil, fmt.Errorf("line %d is not canonical JSON.stringify output", n)
		}
		c, err := demoCaptureFromValue(v, true)
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", n, err)
		}
		cases = append(cases, c)
	}
	if err := demoValidateCases(cases); err != nil {
		return nil, err
	}
	return cases, nil
}

func loadDemoArtifactCases(t *testing.T) []demoartifactstest.Capture {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(demoArtifactsDir, "baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDemoArtifactsBaseline(raw); err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile(filepath.Join(demoArtifactsDir, "golden.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	cases, err := parseDemoArtifactCases(golden, demoArtifactsMaxLine)
	if err != nil {
		t.Fatal(err)
	}
	return cases
}

// ---- path materialization and complete comparison -------------------------

// demoMaterializePath turns the captured "<TMP>/rel/path" form into the actual
// per-case path with the platform's own path rules.
func demoMaterializePath(suffix, root string) string {
	suffix = strings.TrimPrefix(suffix, "/")
	if suffix == "" {
		return root
	}
	return nodefs.Resolve(root, suffix)
}

// demoSlotPath is the only shape a captured root-relative path suffix may have:
// slash-separated segments of plain name characters. Prose after a path (a
// space, punctuation) is not part of the slot, so it fails instead of being
// swallowed into a rewritten path.
var demoSlotPath = regexp.MustCompile(`^(/[A-Za-z0-9._-]+)*$`)

// demoMaterializeSlot rewrites the one known path slot at the end of s: s must
// be prefix + "<TMP>" + a clean path suffix, with exactly one placeholder.
// Everything before the placeholder is kept byte for byte.
func demoMaterializeSlot(s, root string) (string, error) {
	i := strings.Index(s, demoTmp)
	if i < 0 {
		return "", fmt.Errorf("no root placeholder in %q", s)
	}
	prefix, suffix := s[:i], s[i+len(demoTmp):]
	if strings.Contains(suffix, demoTmp) {
		return "", fmt.Errorf("more than one root placeholder in %q", s)
	}
	if !demoSlotPath.MatchString(suffix) {
		return "", fmt.Errorf("text after the root placeholder is not a clean path: %q", suffix)
	}
	return prefix + demoMaterializePath(suffix, root), nil
}

// demoMaterializeError is call.error: a refusal whose path is its last element
// (the known suffix slot). A message without the placeholder is unchanged.
func demoMaterializeError(s, root string) (string, error) {
	if !strings.Contains(s, demoTmp) {
		return s, nil
	}
	return demoMaterializeSlot(s, root)
}

// demoMaterializeStdout is a CLI success stream: only the second line,
// "Preview: <TMP>/path", carries a path. A placeholder anywhere else fails.
func demoMaterializeStdout(s, root string) (string, error) {
	if !strings.Contains(s, demoTmp) {
		return s, nil
	}
	lines := strings.SplitAfter(s, "\n")
	for i, line := range lines {
		if !strings.Contains(line, demoTmp) {
			continue
		}
		body, nl := strings.TrimSuffix(line, "\n"), strings.HasSuffix(line, "\n")
		if i != 1 || !strings.HasPrefix(body, "Preview: ") {
			return "", fmt.Errorf("root placeholder outside the second Preview line (line %d): %q", i+1, line)
		}
		actual, err := demoMaterializeSlot(body, root)
		if err != nil {
			return "", err
		}
		if nl {
			actual += "\n"
		}
		lines[i] = actual
	}
	return strings.Join(lines, ""), nil
}

// demoMaterializeStderr is a CLI failure stream: only the first line,
// "FaultLine error: <message>: <TMP>/path", carries a path. A placeholder on
// any other line fails.
func demoMaterializeStderr(s, root string) (string, error) {
	if !strings.Contains(s, demoTmp) {
		return s, nil
	}
	lines := strings.SplitAfter(s, "\n")
	for i, line := range lines {
		if !strings.Contains(line, demoTmp) {
			continue
		}
		body, nl := strings.TrimSuffix(line, "\n"), strings.HasSuffix(line, "\n")
		if i != 0 || !strings.HasPrefix(body, "FaultLine error: ") {
			return "", fmt.Errorf("root placeholder outside the first error line (line %d): %q", i+1, line)
		}
		actual, err := demoMaterializeSlot(body, root)
		if err != nil {
			return "", err
		}
		if nl {
			actual += "\n"
		}
		lines[i] = actual
	}
	return strings.Join(lines, ""), nil
}

// demoMaterializeValue rewrites only the writer result's directory member, then
// re-serializes in the original key order.
func demoMaterializeValue(valueJSON, kind, root string) (string, error) {
	if kind != "writer" {
		if strings.Contains(valueJSON, demoTmp) {
			return "", fmt.Errorf("%s value holds a root placeholder", kind)
		}
		return valueJSON, nil
	}
	v, err := jsjson.Parse(valueJSON)
	if err != nil {
		return "", err
	}
	if err := demoObjectKeys(v, "writer result", []string{"directory", "manifestDigest", "rootDigest"}); err != nil {
		return "", err
	}
	dir, err := demoString(v.Get("directory"), "directory")
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(dir, demoTmp) {
		return "", fmt.Errorf("directory %q has no root placeholder", dir)
	}
	actual, err := demoMaterializeSlot(dir, root)
	if err != nil {
		return "", err
	}
	v.Obj().Set("directory", jsjson.MakeString(actual))
	return jsjson.Stringify(v), nil
}

// materializeDemoCapture is a committed capture with every known root
// placeholder replaced by the actual per-case root.
func materializeDemoCapture(c demoartifactstest.Capture, root string) (demoartifactstest.Capture, error) {
	out := c
	if c.Call != nil {
		call := *c.Call
		if call.Error != nil {
			m, err := demoMaterializeError(*call.Error, root)
			if err != nil {
				return out, err
			}
			call.Error = &m
		}
		if call.ValueJSON != nil {
			m, err := demoMaterializeValue(*call.ValueJSON, c.Request.Kind, root)
			if err != nil {
				return out, err
			}
			call.ValueJSON = &m
		}
		out.Call = &call
	}
	if c.Process != nil {
		p := *c.Process
		var err error
		if p.Stdout, err = demoMaterializeStdout(p.Stdout, root); err != nil {
			return out, err
		}
		if p.Stderr, err = demoMaterializeStderr(p.Stderr, root); err != nil {
			return out, err
		}
		out.Process = &p
	}
	return out, nil
}

func demoPtr(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%q", *s)
}

func diffDemoInventory(label string, want, got []demoartifactstest.Entry) []string {
	var out []string
	if len(want) != len(got) {
		out = append(out, fmt.Sprintf("%s: %d entries, want %d", label, len(got), len(want)))
	}
	for i := 0; i < len(want) && i < len(got); i++ {
		w, g := want[i], got[i]
		if w.Path != g.Path || w.Kind != g.Kind || demoPtr(w.Base64) != demoPtr(g.Base64) || demoPtr(w.Target) != demoPtr(g.Target) {
			out = append(out, fmt.Sprintf("%s[%d]: got %s %s base64=%s target=%s, want %s %s base64=%s target=%s", label, i,
				g.Kind, g.Path, trimDemo(demoPtr(g.Base64)), demoPtr(g.Target), w.Kind, w.Path, trimDemo(demoPtr(w.Base64)), demoPtr(w.Target)))
			break
		}
	}
	return out
}

func trimDemo(s string) string {
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}

// diffDemoCaptures is the complete comparison: schema, request, the call or
// process (exactly one side), and both full inventories. Nothing is skipped.
func diffDemoCaptures(want, got demoartifactstest.Capture) []string {
	var out []string
	if want.Schema != got.Schema {
		out = append(out, fmt.Sprintf("schema %q, want %q", got.Schema, want.Schema))
	}
	if wr, gr := fmt.Sprintf("%+v", demoRequestText(want.Request)), fmt.Sprintf("%+v", demoRequestText(got.Request)); wr != gr {
		out = append(out, fmt.Sprintf("request %s, want %s", gr, wr))
	}
	switch {
	case (want.Call == nil) != (got.Call == nil):
		out = append(out, fmt.Sprintf("call present=%v, want %v", got.Call != nil, want.Call != nil))
	case want.Call != nil:
		if demoPtr(want.Call.ValueJSON) != demoPtr(got.Call.ValueJSON) {
			out = append(out, fmt.Sprintf("call.valueJSON %s, want %s", trimDemo(demoPtr(got.Call.ValueJSON)), trimDemo(demoPtr(want.Call.ValueJSON))))
		}
		if demoPtr(want.Call.Error) != demoPtr(got.Call.Error) {
			out = append(out, fmt.Sprintf("call.error %s, want %s", demoPtr(got.Call.Error), demoPtr(want.Call.Error)))
		}
	}
	switch {
	case (want.Process == nil) != (got.Process == nil):
		out = append(out, fmt.Sprintf("process present=%v, want %v", got.Process != nil, want.Process != nil))
	case want.Process != nil:
		if want.Process.Stdout != got.Process.Stdout {
			out = append(out, fmt.Sprintf("stdout %q, want %q", got.Process.Stdout, want.Process.Stdout))
		}
		if want.Process.Stderr != got.Process.Stderr {
			out = append(out, fmt.Sprintf("stderr %q, want %q", got.Process.Stderr, want.Process.Stderr))
		}
		if want.Process.ExitCode != got.Process.ExitCode {
			out = append(out, fmt.Sprintf("exit %d, want %d", got.Process.ExitCode, want.Process.ExitCode))
		}
	}
	out = append(out, diffDemoInventory("before", want.Before, got.Before)...)
	out = append(out, diffDemoInventory("after", want.After, got.After)...)
	return out
}

func demoRequestText(r demoartifactstest.Request) string {
	b, _ := json.Marshal(r) // control metadata only
	return string(b)
}

// ---- executing one case ----------------------------------------------------

// demoCaseRoot is a fresh, empty, symlink-free per-case root.
func demoCaseRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// invokeDemoCase runs one request in root with the process cwd moved there,
// restored on return. Cases run serially: cwd is process-wide, so no caller
// is parallel. An AdapterError is a test failure, never a captured result.
func invokeDemoCase(t *testing.T, root, repo string, req demoartifactstest.Request) (got demoartifactstest.Capture) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatalf("restoring cwd: %v", err)
		}
		if r := recover(); r != nil {
			if a, ok := r.(demoartifactstest.AdapterError); ok {
				t.Fatalf("adapter failure for %s: %v", req.ID, a)
			}
			panic(r)
		}
	}()
	// CLI requests run the instrumented flBinary as a child (inherited
	// GOCOVERDIR), never cli.Run in this process.
	return demoartifactstest.Invoke(root, repo, flBinary, req)
}

// demoPrepare performs a case's setup and returns the setup inventory. A
// Windows runner that cannot create fixture-local links may skip exactly the
// named link cases, nothing else.
func demoPrepare(t *testing.T, root, repo string, req demoartifactstest.Request) []demoartifactstest.Entry {
	t.Helper()
	if err := demoartifactstest.Prepare(root, repo, req); err != nil {
		if errors.Is(err, demoartifactstest.ErrSymlinkUnavailable) && runtime.GOOS == "windows" && demoWindowsLinkCases[req.ID] {
			t.Skipf("fixture-local symbolic link creation unavailable on this Windows host: %v", err)
		}
		t.Fatalf("prepare %s: %v", req.ID, err)
	}
	setup, err := demoartifactstest.Inventory(root)
	if err != nil {
		t.Fatal(err)
	}
	return setup
}

// checkDemoDanglingGo asserts the approved Go policy for the one case where
// raw TS differs: the dangling output link is refused, the stdout stays empty
// and the tree (and the absent target) is exactly as set up.
func checkDemoDanglingGo(t *testing.T, root string, committed, got demoartifactstest.Capture) {
	t.Helper()
	// The raw TS record is retained, not edited: TS followed the link and created the target.
	if committed.Process == nil || committed.Process.ExitCode != 0 ||
		!strings.HasPrefix(committed.Process.Stdout, "FaultLine deterministic static judge preview written.\n") ||
		len(committed.After) != 2 || committed.After[1].Path != "target.html" {
		t.Fatalf("raw TS dangling-link capture changed: %+v after=%+v", committed.Process, committed.After)
	}
	if got.Process == nil || got.Call != nil {
		t.Fatalf("CLI case returned %+v", got)
	}
	wantErr := "FaultLine error: Judge preview output must be a regular non-symlink file: " + nodefs.Resolve(root, "out.html") + "\nNext: pnpm fl help\n"
	if got.Process.Stdout != "" || got.Process.Stderr != wantErr || got.Process.ExitCode != 1 {
		t.Fatalf("Go dangling-link result stdout=%q stderr=%q exit=%d, want empty stdout, %q, exit 1", got.Process.Stdout, got.Process.Stderr, got.Process.ExitCode, wantErr)
	}
	if d := diffDemoInventory("after", committed.Before, got.After); len(d) != 0 {
		t.Fatalf("tree changed by the refusal: %v", d)
	}
	if _, err := os.Lstat(filepath.Join(root, "target.html")); !os.IsNotExist(err) {
		t.Fatalf("dangling target exists: %v", err)
	}
}

func TestDemoArtifactsReplay(t *testing.T) {
	repo := oracle.RepoRoot(t)
	for _, committed := range loadDemoArtifactCases(t) {
		committed := committed
		t.Run(committed.Request.ID, func(t *testing.T) {
			root := demoCaseRoot(t)
			setup := demoPrepare(t, root, repo, committed.Request)
			if d := diffDemoInventory("setup", committed.Before, setup); len(d) != 0 {
				t.Fatalf("setup differs from the capture's before: %v", d)
			}
			got := invokeDemoCase(t, root, repo, committed.Request)
			if committed.Request.ID == "cli-dangling-output" {
				checkDemoDanglingGo(t, root, committed, got)
				return
			}
			want, err := materializeDemoCapture(committed, root)
			if err != nil {
				t.Fatal(err)
			}
			if d := diffDemoCaptures(want, got); len(d) != 0 {
				t.Fatalf("%s differs from the frozen-TS capture:\n%s", committed.Request.ID, strings.Join(d, "\n"))
			}
		})
	}
}

// ---- baseline and loader corruption ----------------------------------------

func TestDemoArtifactsBaseline(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(demoArtifactsDir, "baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDemoArtifactsBaseline(raw); err != nil {
		t.Fatal(err)
	}
	var original map[string]any
	if err := json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	if len(original) != 6 {
		t.Fatalf("committed baseline has %d fields, want the six declared", len(original))
	}
	marshal := func(m map[string]any) []byte {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	for key := range demoWantBaseline {
		t.Run("metadata/"+key, func(t *testing.T) {
			changed, missing, wrongType := map[string]any{}, map[string]any{}, map[string]any{}
			for k, v := range original {
				changed[k], wrongType[k] = v, v
				if k != key {
					missing[k] = v
				}
			}
			changed[key], wrongType[key] = "unexpected", 7
			for name, bad := range map[string]map[string]any{"changed": changed, "missing": missing, "non-string": wrongType} {
				if validateDemoArtifactsBaseline(marshal(bad)) == nil {
					t.Errorf("accepted %s %s", name, key)
				}
			}
		})
	}
	extra := map[string]any{"unreviewed": "value"}
	for k, v := range original {
		extra[k] = v
	}
	if validateDemoArtifactsBaseline(marshal(extra)) == nil {
		t.Error("accepted an extra metadata field")
	}
	for _, bad := range []string{"", "null", "{}", "[]", "{", string(raw) + "{}", string(raw) + "x"} {
		if validateDemoArtifactsBaseline([]byte(bad)) == nil {
			t.Errorf("accepted metadata %q", bad)
		}
	}
}

// TestDemoArtifactsBaselineDuplicateFields: encoding/json keeps the last of
// duplicate object members, so a duplicate could hide a corrupted provenance
// value behind a valid one. Exactly six semantic fields may exist, each once.
func TestDemoArtifactsBaselineDuplicateFields(t *testing.T) {
	const (
		schema  = `"schema":"faultline.demo-artifacts-oracle.v1"`
		rest    = `,"sourceCommit":"ce99a793af0b020bf1b9cf21114f8d7897b37207","manifestCommit":"647440884a6668042f7726f2cbade38886d00a41","manifestVersion":"0.1.4","nodeVersion":"22.22.2","cachedRuntimeVersion":"v22.22.2"`
		corrupt = `"schema":"faultline.demo-artifacts-oracle.v0"`
	)
	if err := validateDemoArtifactsBaseline([]byte("{" + schema + rest + "}")); err != nil {
		t.Fatalf("the single-occurrence control must pass: %v", err)
	}
	for name, raw := range map[string]string{
		"corrupt value first, valid value last (last wins)": "{" + corrupt + "," + schema + rest + "}",
		"valid value first, corrupt value last":             "{" + schema + "," + corrupt + rest + "}",
		"identical duplicate":                               "{" + schema + "," + schema + rest + "}",
		"duplicate of a later field":                        "{" + schema + rest + `,"nodeVersion":"22.22.2"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if validateDemoArtifactsBaseline([]byte(raw)) == nil {
				t.Fatalf("accepted duplicate member: %s", raw)
			}
		})
	}
}

func TestDemoArtifactsGoldenCorruption(t *testing.T) {
	golden, err := os.ReadFile(filepath.Join(demoArtifactsDir, "golden.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseDemoArtifactCases(golden, demoArtifactsMaxLine); err != nil {
		t.Fatalf("the committed corpus must load: %v", err)
	}
	lines := strings.SplitAfter(string(golden), "\n")
	lines = lines[:len(lines)-1]
	if len(lines) != len(demoArtifactMatrix) {
		t.Fatalf("committed corpus has %d records, want %d", len(lines), len(demoArtifactMatrix))
	}
	join := func(ls []string) []byte { return []byte(strings.Join(ls, "")) }
	with := func(i int, line string) []byte {
		ls := append([]string(nil), lines...)
		ls[i] = line
		return join(ls)
	}
	writerLine := strings.Index(string(golden), `"id":"writer-replay"`)
	if writerLine < 0 {
		t.Fatal("no writer-replay record")
	}
	// Record indexes by ID (the matrix order).
	idx := map[string]int{}
	for i, m := range demoArtifactMatrix {
		idx[m.id] = i
	}
	rowOf := func(id string) string { return strings.TrimSuffix(lines[idx[id]], "\n") }
	replace := func(id, old, new string) []byte {
		row := rowOf(id)
		if !strings.Contains(row, old) {
			t.Fatalf("%s: fixture text %q not found", id, old)
		}
		return with(idx[id], strings.Replace(row, old, new, 1)+"\n")
	}
	const (
		procJSON = `{"stdout":"x","stderr":"","exitCode":0}`
		callJSON = `{"valueJSON":"1","error":null}`
	)
	// setMember re-serializes a committed row with the given top-level members
	// replaced (position and every other byte kept), so the corruption is the
	// intended shape and not a stray extra key.
	setMember := func(id string, members map[string]string) []byte {
		v, err := jsjson.Parse(rowOf(id))
		if err != nil {
			t.Fatal(err)
		}
		for key, text := range members {
			member, err := jsjson.Parse(text)
			if err != nil {
				t.Fatal(err)
			}
			v.Obj().Set(key, member)
		}
		return with(idx[id], jsjson.Stringify(v)+"\n")
	}
	corruptions := map[string][]byte{
		"empty file":            nil,
		"blank record":          with(3, "\n"),
		"null record":           with(3, "null\n"),
		"truncated record":      with(3, rowOf("render-rerun-refused")[:40]+"\n"),
		"trailing junk":         with(3, rowOf("render-rerun-refused")+"x\n"),
		"trailing object":       with(3, rowOf("render-rerun-refused")+"{}\n"),
		"CR line ending":        with(3, rowOf("render-rerun-refused")+"\r\n"),
		"leading space":         with(3, " "+rowOf("render-rerun-refused")+"\n"),
		"no final newline":      []byte(strings.TrimSuffix(string(golden), "\n")),
		"extra blank line":      append(append([]byte(nil), golden...), '\n'),
		"missing record":        join(append(append([]string(nil), lines[:5]...), lines[6:]...)),
		"extra record":          append(append([]byte(nil), golden...), []byte(strings.TrimSuffix(lines[0], "\n")+"\n")...),
		"duplicate case ID":     with(2, lines[1]),
		"swapped record order":  join(append(append(append([]string(nil), lines[:1]...), lines[2], lines[1]), lines[3:]...)),
		"unknown case ID":       replace("render-default", `"id":"render-default"`, `"id":"render-unknown"`),
		"wrong schema":          replace("render-default", `faultline.demo-artifacts-case.v1`, `faultline.demo-artifacts-case.v2`),
		"missing key":           replace("render-default", `"process":null,`, ``),
		"extra key":             replace("render-default", `"process":null,`, `"process":null,"zz":1,`),
		"unknown request field": replace("render-default", `"kind":"render"`, `"kind":"render","zz":1`),
		"changed request kind":  replace("render-default", `"kind":"render"`, `"kind":"writer"`),
		"null before":           replace("render-default", `"before":[]`, `"before":null`),
		"null after":            replace("render-default", `"after":[]`, `"after":null`),
		// The call/process discriminator, one real shape each. The library
		// and CLI controls (the unmodified committed rows) are accepted above.
		"library with neither call nor process": setMember("render-default", map[string]string{"call": "null"}),
		"library with a process instead":        setMember("render-default", map[string]string{"call": "null", "process": procJSON}),
		"library with call and process":         setMember("render-default", map[string]string{"process": procJSON}),
		"cli with neither call nor process":     setMember("cli-default", map[string]string{"process": "null"}),
		"cli with a call instead of a process":  setMember("cli-default", map[string]string{"call": callJSON, "process": "null"}),
		"cli with call and process":             setMember("cli-default", map[string]string{"call": callJSON}),
		"both value and error":                  replace("render-rerun-refused", `"valueJSON":null`, `"valueJSON":"1"`),
		"neither value nor err":                 replace("render-rerun-refused", `"error":"Judge preview only renders the deterministic REPLAY fixture."`, `"error":null`),
		"invalid base64":                        replace("cli-default", `"kind":"file","base64":"`, `"kind":"file","base64":"!`),
		"non-canonical base64":                  replace("cli-overwrite", `"base64":"b2xkIHByZXZpZXcK"`, `"base64":"YR=="`),
		"file without base64":                   replace("cli-overwrite", `"kind":"file","base64":"b2xkIHByZXZpZXcK"`, `"kind":"file"`),
		"unsorted inventory":                    swapInventory(t, rowOf("cli-default"), idx["cli-default"], lines),
		"symlink without target":                replace("cli-dangling-output", `"kind":"symlink","target":"target.html"`, `"kind":"symlink"`),
		"absolute link target":                  replace("cli-dangling-output", `"target":"target.html"`, `"target":"/target.html"`),
		"exit not an integer":                   replace("cli-empty-output", `"exitCode":1`, `"exitCode":1.5`),
		"empty process capture":                 replace("cli-empty-output", `"stderr":"FaultLine error: Missing required option: --output\nNext: pnpm fl help\n"`, `"stderr":""`),
		// Whitespace between tokens: valid JSON of the same value, not
		// canonical JSON.stringify output.
		"non-canonical whitespace": replace("render-default", `"id":"render-default"`, `"id" : "render-default"`),
		// The same string spelled with an escape JSON.stringify never emits.
		"non-canonical escape spelling": replace("render-default", `"id":"render-default"`, "\"id\":\"render-d\\u0065fault\""),
	}
	for name, bad := range corruptions {
		t.Run(name, func(t *testing.T) {
			if cases, err := parseDemoArtifactCases(bad, demoArtifactsMaxLine); err == nil {
				t.Fatalf("accepted a corrupted corpus (%d records)", len(cases))
			}
		})
	}
	t.Run("oversized record is rejected, not truncated", func(t *testing.T) {
		if _, err := parseDemoArtifactCases(golden, 1<<10); err == nil {
			t.Fatal("a record longer than the scanner limit was accepted")
		}
	})
}

// TestDemoArtifactsScannerLimit: the configured per-line limit is the limit.
// Every line here is far below 64 KiB, so a scanner whose initial buffer
// capacity silently raises the limit to 64 KiB accepts the over-limit line.
func TestDemoArtifactsScannerLimit(t *testing.T) {
	corpus := []byte("aaaa\n" + strings.Repeat("b", 5000) + "\ncccc\n")
	t.Run("a line over a smaller configured limit is a token overflow", func(t *testing.T) {
		_, err := scanDemoArtifactLines(corpus, 1000)
		if !errors.Is(err, bufio.ErrTooLong) {
			t.Fatalf("limit 1000 on a 5000-byte line: err = %v, want bufio.ErrTooLong", err)
		}
	})
	t.Run("a sufficient limit accepts the same corpus", func(t *testing.T) {
		lines, err := scanDemoArtifactLines(corpus, 6000)
		if err != nil || len(lines) != 3 || len(lines[1]) != 5000 {
			t.Fatalf("limit 6000: %d lines, err = %v", len(lines), err)
		}
	})
	t.Run("the real 32 MiB bound", func(t *testing.T) {
		// Not JSON: the rejection is the scanner's, not a malformed-record error.
		over := append(bytes.Repeat([]byte{'x'}, demoArtifactsMaxLine+1), '\n')
		if _, err := scanDemoArtifactLines(over, demoArtifactsMaxLine); !errors.Is(err, bufio.ErrTooLong) {
			t.Fatalf("a line of 32 MiB + 1 bytes: err = %v, want bufio.ErrTooLong", err)
		}
		fits := append(bytes.Repeat([]byte{'x'}, demoArtifactsMaxLine-1), '\n')
		lines, err := scanDemoArtifactLines(fits, demoArtifactsMaxLine)
		if err != nil || len(lines) != 1 || len(lines[0]) != demoArtifactsMaxLine-1 {
			t.Fatalf("a line of 32 MiB - 1 bytes: %d lines, err = %v", len(lines), err)
		}
	})
}

// swapInventory returns the corpus with the first two entries of the record's
// after inventory exchanged.
func swapInventory(t *testing.T, row string, at int, lines []string) []byte {
	t.Helper()
	i := strings.Index(row, `"after":[`)
	if i < 0 {
		t.Fatal("no after inventory")
	}
	body := row[i+len(`"after":[`):]
	end := strings.Index(body, `},{`)
	if end < 0 {
		t.Fatal("inventory has fewer than two entries")
	}
	first := body[:end+1]
	rest := body[end+2:]
	second := rest[:strings.Index(rest, `}`)+1]
	if strings.Contains(second, `"path"`) == false {
		t.Fatalf("unexpected second entry %q", second)
	}
	swapped := row[:i] + `"after":[` + second + `,` + first + body[end+1+len(second)+1:]
	ls := append([]string(nil), lines...)
	ls[at] = swapped + "\n"
	return []byte(strings.Join(ls, ""))
}

// TestDemoArtifactsComparisonIsComplete: the replay comparator is not a
// length-only or partial check. Every member a replay must compare is changed
// alone and has to be reported, and an empty actual result differs from every
// committed case.
func TestDemoArtifactsComparisonIsComplete(t *testing.T) {
	cases := loadDemoArtifactCases(t)
	for _, c := range cases {
		if d := diffDemoCaptures(c, demoartifactstest.Capture{}); len(d) == 0 {
			t.Errorf("%s: an empty actual result was not reported", c.Request.ID)
		}
	}
	byID := map[string]demoartifactstest.Capture{}
	for _, c := range cases {
		byID[c.Request.ID] = c
	}
	str := func(s string) *string { return &s }
	mutations := []struct {
		name string
		id   string
		edit func(c *demoartifactstest.Capture)
	}{
		{"schema", "render-default", func(c *demoartifactstest.Capture) { c.Schema = "x" }},
		{"request id", "render-default", func(c *demoartifactstest.Capture) { c.Request.ID = "x" }},
		{"call error text", "render-rerun-refused", func(c *demoartifactstest.Capture) { c.Call.Error = str(*c.Call.Error + ".") }},
		{"call value one byte", "render-default", func(c *demoartifactstest.Capture) { c.Call.ValueJSON = str(*c.Call.ValueJSON + " ") }},
		{"call became error", "render-default", func(c *demoartifactstest.Capture) { c.Call = &demoartifactstest.Call{Error: str("x")} }},
		{"stdout", "cli-default", func(c *demoartifactstest.Capture) { c.Process.Stdout += "x" }},
		{"stderr", "cli-extension", func(c *demoartifactstest.Capture) { c.Process.Stderr += "x" }},
		{"exit code", "cli-extension", func(c *demoartifactstest.Capture) { c.Process.ExitCode = 0 }},
		{"process dropped", "cli-extension", func(c *demoartifactstest.Capture) { c.Process = nil }},
		{"before entry removed", "cli-overwrite", func(c *demoartifactstest.Capture) { c.Before = c.Before[:0] }},
		{"after entry removed", "cli-default", func(c *demoartifactstest.Capture) { c.After = c.After[:len(c.After)-1] }},
		{"after file byte", "cli-default", func(c *demoartifactstest.Capture) {
			c.After = append([]demoartifactstest.Entry(nil), c.After...)
			b := base64.StdEncoding.EncodeToString([]byte("x"))
			c.After[1].Base64 = &b
		}},
		{"after link target", "cli-dangling-output", func(c *demoartifactstest.Capture) {
			c.After = append([]demoartifactstest.Entry(nil), c.After...)
			c.After[0].Target = str("elsewhere")
		}},
		{"after entry kind", "cli-default", func(c *demoartifactstest.Capture) {
			c.After = append([]demoartifactstest.Entry(nil), c.After...)
			c.After[0].Kind = "file"
		}},
	}
	for _, m := range mutations {
		t.Run(m.name, func(t *testing.T) {
			want := byID[m.id]
			// A deep copy of the members the mutation may touch.
			got := want
			if want.Call != nil {
				call := *want.Call
				got.Call = &call
			}
			if want.Process != nil {
				p := *want.Process
				got.Process = &p
			}
			got.Before = append([]demoartifactstest.Entry(nil), want.Before...)
			got.After = append([]demoartifactstest.Entry(nil), want.After...)
			if d := diffDemoCaptures(want, got); len(d) != 0 {
				t.Fatalf("an unmodified copy differs: %v", d)
			}
			m.edit(&got)
			if d := diffDemoCaptures(want, got); len(d) == 0 {
				t.Fatalf("the %s change was not reported", m.name)
			}
		})
	}
}

// TestDemoArtifactsMaterialization: root substitution is limited to the known
// path locations. Slashes elsewhere are never rewritten.
func TestDemoArtifactsMaterialization(t *testing.T) {
	root := nodefs.Resolve(t.TempDir())
	p := func(rel string) string { return nodefs.Resolve(root, rel) }

	t.Run("error: the path suffix slot only", func(t *testing.T) {
		got, err := demoMaterializeError("Proof bundle output must be a directory: <TMP>/.faultline/bundles/demo", root)
		if want := "Proof bundle output must be a directory: " + p(".faultline/bundles/demo"); err != nil || got != want {
			t.Errorf("got %q (%v), want %q", got, err, want)
		}
		if got, err := demoMaterializeError("a/b/c <>&", root); err != nil || got != "a/b/c <>&" {
			t.Errorf("text without a placeholder changed: %q (%v)", got, err)
		}
		for name, bad := range map[string]string{
			"trailing prose on the same line": "refused <TMP>/a/b.html and then more text",
			"second path slot":                "moved <TMP>/a to <TMP>/b",
			"placeholder not at a boundary":   "x <TMP>y",
		} {
			if got, err := demoMaterializeError(bad, root); err == nil {
				t.Errorf("%s: accepted %q -> %q", name, bad, got)
			}
		}
	})
	t.Run("stdout: only the second Preview line", func(t *testing.T) {
		in := "FaultLine deterministic static judge preview written.\nPreview: <TMP>/docs/judge-preview.html\nBytes: 19265\nLimitation: <TMP>/not-a-slot\n"
		if _, err := demoMaterializeStdout(in, root); err == nil {
			t.Error("a placeholder on the Limitation line was rewritten")
		}
		ok := "FaultLine deterministic static judge preview written.\nPreview: <TMP>/docs/judge-preview.html\nBytes: 19265\nLimitation: a/b/c <>&\n"
		got, err := demoMaterializeStdout(ok, root)
		want := "FaultLine deterministic static judge preview written.\nPreview: " + p("docs/judge-preview.html") + "\nBytes: 19265\nLimitation: a/b/c <>&\n"
		if err != nil || got != want {
			t.Errorf("got %q (%v), want %q", got, err, want)
		}
		for name, bad := range map[string]string{
			"placeholder on the first line":       "Preview: <TMP>/x\nPreview: <TMP>/y\n",
			"trailing prose on the Preview line":  "intro\nPreview: <TMP>/a/b.html (written)\nBytes: 1\n",
			"second placeholder on that line":     "intro\nPreview: <TMP>/a <TMP>/b\n",
			"a placeholder not on a Preview line": "intro\nWritten: <TMP>/a\n",
		} {
			if _, err := demoMaterializeStdout(bad, root); err == nil {
				t.Errorf("%s: accepted %q", name, bad)
			}
		}
	})
	t.Run("stderr: only the first FaultLine error line", func(t *testing.T) {
		in := "FaultLine error: Judge preview output must be a regular non-symlink file: <TMP>/out.html\nNext: pnpm fl help\n"
		got, err := demoMaterializeStderr(in, root)
		want := "FaultLine error: Judge preview output must be a regular non-symlink file: " + p("out.html") + "\nNext: pnpm fl help\n"
		if err != nil || got != want {
			t.Errorf("got %q (%v), want %q", got, err, want)
		}
		for name, bad := range map[string]string{
			"placeholder on the hint line":       "FaultLine error: x: <TMP>/a\nNext: <TMP>/b\n",
			"trailing prose on the error line":   "FaultLine error: x: <TMP>/a/b.html then\nNext: pnpm fl help\n",
			"a line that is not the error line":  "other: <TMP>/a\n",
			"two placeholders on the error line": "FaultLine error: <TMP>/a <TMP>/b\n",
		} {
			if _, err := demoMaterializeStderr(bad, root); err == nil {
				t.Errorf("%s: accepted %q", name, bad)
			}
		}
	})
	if _, err := demoMaterializeValue(`{"directory":"/not/placeholder","manifestDigest":"a","rootDigest":"b"}`, "writer", root); err == nil {
		t.Error("a writer directory without the placeholder was accepted")
	}
	if _, err := demoMaterializeValue(`{"directory":"<TMP>/x","rootDigest":"b","manifestDigest":"a","extra":1}`, "writer", root); err == nil {
		t.Error("a writer result with an extra member was accepted")
	}
	v, err := demoMaterializeValue(`{"directory":"<TMP>/d","manifestDigest":"<TMP>/m","rootDigest":"r"}`, "writer", root)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"directory":` + jsjson.Quote(nodefs.Resolve(root, "d")) + `,"manifestDigest":"<TMP>/m","rootDigest":"r"}`; v != want {
		t.Errorf("only the directory member may change: got %s, want %s", v, want)
	}
	if _, err := demoMaterializeValue(`"<TMP>/x"`, "render", root); err == nil {
		t.Error("a renderer value holding a root placeholder was accepted")
	}
}
