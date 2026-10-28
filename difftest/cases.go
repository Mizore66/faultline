//go:build faultline_test

package difftest

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
)

type template struct{ fields *jsjson.Obj }

func (t template) str(k string) string { return t.fields.Field(k).Str() }

type testCase struct {
	id, base, file string
	tpl            *template
}

func listFiles(root, rel string) []string {
	names, _ := nodefs.ReadDirNames(filepath.Join(root, rel))
	var out []string
	for _, name := range names {
		r := name
		if rel != "" {
			r = rel + "/" + name
		}
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(r)))
		if err != nil {
			continue
		}
		if info.IsDir() {
			out = append(out, listFiles(root, r)...)
		} else if info.Mode().IsRegular() {
			out = append(out, r)
		}
	}
	slices.Sort(out)
	return out
}

func parseObjectFile(path string) (*jsjson.Obj, bool) {
	text, err := nodefs.ReadText(path)
	if err != nil {
		return nil, false
	}
	v, err := jsjson.Parse(text)
	if err != nil || v.Kind() != jsjson.Object {
		return nil, false
	}
	return v.Obj(), true
}

func applicable(t template, path string) bool {
	b, _ := os.ReadFile(path)
	switch t.str("op") {
	case "flip-byte":
		return len(b) > 0
	case "replace-text":
		return bytes.Contains(b, latin1(t.str("find")))
	case "strip-trailing-newline":
		return len(b) > 0 && b[len(b)-1] == '\n'
	case "json-add-key":
		_, ok := parseObjectFile(path)
		return ok
	case "json-retype", "json-drop-first":
		o, ok := parseObjectFile(path)
		return ok && o.Len() > 0
	case "json-edit":
		return jsonEditApplicable(t, path)
	case "replace-nested":
		return bytes.Contains(b, latin1(t.str("find")))
	}
	return true
}

func (t template) jsonPath(k string) []jsjson.Value { return t.fields.Field(k).Items() }

// at walks a JSON path (object keys and array indices); ok is false when absent.
func at(v jsjson.Value, path []jsjson.Value) (jsjson.Value, bool) {
	for _, k := range path {
		switch v.Kind() {
		case jsjson.Array:
			if k.Kind() != jsjson.Number || int(k.Num()) >= len(v.Items()) {
				return jsjson.Value{}, false
			}
			v = v.Items()[int(k.Num())]
		case jsjson.Object:
			child, ok := v.Obj().Get(k.Str())
			if k.Kind() != jsjson.String || !ok {
				return jsjson.Value{}, false
			}
			v = child
		default:
			return jsjson.Value{}, false
		}
	}
	return v, true
}

func setAt(root jsjson.Value, path []jsjson.Value, value jsjson.Value) {
	parent, _ := at(root, path[:len(path)-1])
	k := path[len(path)-1]
	if parent.Kind() == jsjson.Array {
		items, i := parent.Items(), int(k.Num())
		if i >= len(items) {
			// JS `a[i] = v` past the end grows the array; holes stringify as null.
			grown := slices.Clone(items)
			for len(grown) < i {
				grown = append(grown, jsjson.MakeNull())
			}
			setAt(root, path[:len(path)-1], jsjson.MakeArray(append(grown, value)))
			return
		}
		items[i] = value // shares backing storage with the tree
	} else {
		parent.Obj().Set(k.Str(), value)
	}
}

// jsonEdits are a json-edit template's steps: the template itself, or the
// objects in its "edits" list, applied in order.
func jsonEdits(t template) []template {
	list, ok := t.fields.Get("edits")
	if !ok {
		return []template{t}
	}
	out := make([]template, len(list.Items()))
	for i, e := range list.Items() {
		out[i] = template{e.Obj()}
	}
	return out
}

// jsonEditApplicable checks every step against the file as it would be after
// the steps before it.
func jsonEditApplicable(t template, path string) bool {
	o, ok := parseObjectFile(path)
	if !ok {
		return false
	}
	v := jsjson.MakeObject(o)
	for _, e := range jsonEdits(t) {
		p := e.jsonPath("jsonPath")
		if parent, ok := at(v, p[:len(p)-1]); !ok || (parent.Kind() != jsjson.Object && parent.Kind() != jsjson.Array) {
			return false
		}
		has := func(k string) bool { _, ok := e.fields.Get(k); return ok }
		if has("delete") || has("valueFrom") || has("swapWith") {
			if _, ok := at(v, p); !ok {
				return false
			}
		}
		for _, k := range []string{"valueFrom", "swapWith"} {
			if has(k) {
				if _, ok := at(v, e.jsonPath(k)); !ok {
					return false
				}
			}
		}
		v = applyJSONEdit(v, e)
	}
	return true
}

// applyJSONEdit applies one step and returns the (possibly new) root.
func applyJSONEdit(v jsjson.Value, e template) jsjson.Value {
	p := e.jsonPath("jsonPath")
	_, del := e.fields.Get("delete")
	_, swap := e.fields.Get("swapWith")
	_, from := e.fields.Get("valueFrom")
	switch {
	case del:
		parent, _ := at(v, p[:len(p)-1])
		k := p[len(p)-1]
		if parent.Kind() == jsjson.Array {
			items := parent.Items()
			i := int(k.Num())
			rest := append(slices.Clone(items[:i]), items[i+1:]...)
			setAt(v, p[:len(p)-1], jsjson.MakeArray(rest))
		} else {
			parent.Obj().Delete(k.Str())
		}
	case swap:
		a, _ := at(v, p)
		b, _ := at(v, e.jsonPath("swapWith"))
		setAt(v, p, b)
		setAt(v, e.jsonPath("swapWith"), a)
	case from:
		src, _ := at(v, e.jsonPath("valueFrom"))
		setAt(v, p, src)
	default:
		setAt(v, p, cloneJSON(e.fields.Field("value")))
	}
	return v
}

// cloneJSON deep-copies a template value (structuredClone in the TS engine),
// so later steps never edit the template itself.
func cloneJSON(v jsjson.Value) jsjson.Value {
	return bundleMust(jsjson.Parse(jsjson.Stringify(v)))
}

func bundleMust(v jsjson.Value, err error) jsjson.Value {
	if err != nil {
		panic(err)
	}
	return v
}

// matches is a template file pattern: "*", "*.json", "dir/*" or an exact path.
func matches(pattern, file string) bool {
	switch {
	case pattern == "*":
		return true
	case pattern == "*.json":
		return strings.HasSuffix(file, ".json")
	case strings.HasSuffix(pattern, "/*"):
		return strings.HasPrefix(file, pattern[:len(pattern)-1])
	}
	return pattern == file
}

// fixturesDir holds files that copy-file templates put into a bundle.
func fixturesDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata", "fixtures")
}

// pathOps target a path in the bundle (or the bundle itself), not an existing file.
var pathOps = map[string]bool{"add-file": true, "root-symlink": true, "remove-root": true, "fifo": true, "sparse-file": true, "many-files": true, "fake-git": true, "pad-total": true,
	"add-symlink": true, "set-mtime": true}

// latin1 is Buffer.from(s, "latin1") for the ASCII find/replace strings.
func latin1(s string) []byte {
	units := jsstr.ToUTF16(s)
	out := make([]byte, len(units))
	for i, u := range units {
		out[i] = byte(u)
	}
	return out
}

func expandCases(base, root string, templates []template) []testCase {
	files := listFiles(root, "")
	cases := []testCase{{id: base, base: base}}
	for i := range templates {
		t := templates[i]
		// git-shape-* bases pin one history shape each (round 5): only the
		// templates that name them run there, not the generic ones.
		if bases, ok := t.fields.Get("bases"); ok && !slices.ContainsFunc(bases.Items(), func(b jsjson.Value) bool { return b.Str() == base }) {
			continue
		} else if !ok && strings.HasPrefix(base, "git-shape-") {
			continue
		}
		var targets []string
		if pathOps[t.str("op")] {
			target := "."
			if p, ok := t.fields.Get("path"); ok {
				target = p.Str()
			}
			targets = []string{target}
		} else {
			for _, f := range files {
				if matches(t.str("file"), f) {
					targets = append(targets, f)
				}
			}
			if t.fields.Field("first").Bool() && len(targets) > 1 {
				targets = targets[:1]
			}
		}
		for _, file := range targets {
			if !pathOps[t.str("op")] && !applicable(t, filepath.Join(root, filepath.FromSlash(file))) {
				continue
			}
			cases = append(cases, testCase{id: base + "__" + t.str("id") + "__" + strings.ReplaceAll(file, "/", "~"), base: base, file: file, tpl: &templates[i]})
		}
	}
	return cases
}

// resignLedgerJSON is the TS engine's resignLedgerJson: checkpoint digests,
// then the event hash chain from the genesis hash.
func resignLedgerJSON(ledger jsjson.Value) {
	genesis := jsjson.NewObj()
	genesis.Set("kind", jsjson.MakeString("FAULTLINE_CODEX_LIFECYCLE_GENESIS"))
	for _, k := range []string{"schemaVersion", "ledgerId", "sessionId", "createdAt"} {
		genesis.Set(k, ledger.Get(k))
	}
	previous := bundleMustString(canonical.DigestJSON(jsjson.MakeObject(genesis)))
	without := func(o *jsjson.Obj, key string) jsjson.Value {
		c := jsjson.NewObj()
		for _, k := range o.Keys() {
			if k != key {
				c.Set(k, o.Field(k))
			}
		}
		return jsjson.MakeObject(c)
	}
	for _, event := range ledger.Get("events").Items() {
		if event.Get("event", "type").Str() == "WORKTREE_CHECKPOINT" {
			if cp := event.Get("event", "payload", "checkpoint"); cp.Kind() == jsjson.Object {
				cp.Obj().Set("digest", jsjson.MakeString(bundleMustString(canonical.DigestJSON(without(cp.Obj(), "digest")))))
			}
		}
		event.Obj().Set("previousHash", jsjson.MakeString(previous))
		previous = bundleMustString(canonical.DigestJSON(without(event.Obj(), "hash")))
		event.Obj().Set("hash", jsjson.MakeString(previous))
	}
}

// padTotal is the TS engine's padTotal: sparse files path-00000, ... of at
// most 120 MiB (under the per-artifact cap) bring the bundle's regular
// files, hashes.txt and ROOT.sha256 included, to exactly total bytes.
// padTotal pads the bundle with zero files until its regular files add up
// to total bytes; with declared, only the files hashes.txt catalogs count
// (all but hashes.txt and ROOT.sha256), the total the git verifier checks.
func padTotal(root, base, path string, total int64, declared bool) {
	const chunk = 120 << 20
	size := func() int64 {
		var n int64
		for _, f := range listFiles(root, "") {
			if declared && (f == "hashes.txt" || f == "ROOT.sha256") {
				continue
			}
			if st, err := os.Stat(filepath.Join(root, filepath.FromSlash(f))); err == nil {
				n += st.Size()
			}
		}
		return n
	}
	rehash(root, base)
	need := total - size()
	var last string
	for i := 0; need > 0; i++ {
		last = fmt.Sprintf("%s-%05d", path, i)
		n := min(need, chunk)
		os.WriteFile(last, nil, 0o644)
		os.Truncate(last, n)
		need -= n
	}
	rehash(root, base)
	if st, err := os.Stat(last); err == nil {
		os.Truncate(last, st.Size()+total-size())
	}
	rehash(root, base)
}

// rebindLifecycle is the TS engine's rebindLifecycle, the writer's
// bindLifecycleLedger: the binding takes its transport from SESSION_STARTED,
// and where the writer would refuse to bind the manifest keeps its old
// binding (round 5, row 13).
func rebindLifecycle(root string, ledger jsjson.Value) {
	investigation, _ := parseObjectFile(filepath.Join(root, "investigation.json"))
	manifestPath := filepath.Join(root, "manifest.json")
	manifest, _ := parseObjectFile(manifestPath)
	verification := gitproof.VerifyCodexLifecycleLedger(ledger)
	if !verification.Valid || verification.HeadHash == nil {
		return
	}
	events := ledger.Get("events").Items()
	var sessionStarted jsjson.Value
	for _, event := range events {
		if event.Get("event", "type").Str() == "SESSION_STARTED" {
			sessionStarted = event
			break
		}
	}
	if sessionStarted.Kind() != jsjson.Object {
		return
	}
	stateByCommit := map[string]jsjson.Value{}
	states := investigation.Field("states").Items()
	for _, st := range states {
		stateByCommit[st.Get("commit").Str()] = st // new Map(...): the last one wins
	}
	var bindings []jsjson.Value
	covered := map[float64]bool{}
	descendant := false
	for _, event := range events {
		if event.Get("event", "type").Str() != "WORKTREE_CHECKPOINT" {
			continue
		}
		cp := event.Get("event", "payload", "checkpoint")
		state, ok := stateByCommit[cp.Get("headCommit").Str()]
		if !ok {
			continue
		}
		if cp.Get("treeDigest").Str() != state.Get("tree").Str() {
			return
		}
		b := jsjson.NewObj()
		b.Set("sequence", event.Get("sequence"))
		b.Set("stateIndex", state.Get("index"))
		b.Set("checkpointDigest", cp.Get("digest"))
		bindings = append(bindings, jsjson.MakeObject(b))
		covered[state.Get("index").Num()] = true
		d := investigation.Field("resolvedRange").Get("descendant", "index") // undefined matches no index
		descendant = descendant || d.Kind() == jsjson.Number && state.Get("index").Num() == d.Num()
	}
	if len(bindings) == 0 || !descendant {
		return
	}
	status := "PARTIALLY_BOUND"
	if len(covered) == len(states) {
		status = "FULLY_BOUND"
	}
	lifecycle := jsjson.NewObj()
	for _, k := range manifest.Field("lifecycle").Obj().Keys() {
		lifecycle.Set(k, manifest.Field("lifecycle").Get(k))
	}
	lifecycle.Set("status", jsjson.MakeString(status))
	lifecycle.Set("ledgerDigest", jsjson.MakeString(bundleMustString(canonical.DigestJSON(ledger))))
	lifecycle.Set("headHash", jsjson.MakeString(*verification.HeadHash))
	lifecycle.Set("transport", sessionStarted.Get("event", "payload", "transport"))
	lifecycle.Set("checkpointBindings", jsjson.MakeArray(bindings))
	manifest.Set("lifecycle", jsjson.MakeObject(lifecycle))
	writeJSON(manifestPath, jsjson.MakeObject(manifest))
}

func bundleMustString(s string, err error) string {
	if err != nil {
		panic(err)
	}
	return s
}

func writeJSON(path string, v jsjson.Value) {
	os.WriteFile(path, []byte(jsstr.ToUTF8(jsjson.StringifyIndent(v, "  ")+"\n")), 0o644)
}

// writeCanonical writes canonicalJson(v) + "\n", a turn bundle artifact's
// bytes (cases.ts writeCanonical).
func writeCanonical(path string, v jsjson.Value) {
	text, err := canonical.CanonicalJSON(v)
	if err != nil {
		panic(err)
	}
	os.WriteFile(path, []byte(jsstr.ToUTF8(text+"\n")), 0o644)
}

// withoutKey is a copy of o without key (`const { key: _, ...rest } = o`).
func withoutKey(o *jsjson.Obj, key string) jsjson.Value {
	c := jsjson.NewObj()
	for _, k := range o.Keys() {
		if k != key {
			c.Set(k, o.Field(k))
		}
	}
	return jsjson.MakeObject(c)
}

// resignTurnBundle is the TS engine's resignTurnBundle: the editor who
// rewrites a turn bundle consistently after an edit of investigation.json.
// Each runId is re-derived from its run (executionId is left alone) and
// renamed wherever stable states and transitions name it, then the run and
// transition artifacts and the manifest's artifact index and investigation
// digest are rewritten; the template's rehash then rewrites hashes.txt and
// ROOT.sha256. Like TS, which throws, it panics (failing the test) when
// investigation.json or manifest.json does not parse as an object.
func resignTurnBundle(root string) {
	investigationPath := filepath.Join(root, "investigation.json")
	o, ok := parseObjectFile(investigationPath)
	if !ok {
		panic("resignTurn: investigation.json is not a JSON object")
	}
	investigation := jsjson.MakeObject(o)
	renamed := map[string]string{}
	runs := investigation.Get("runs").Items()
	for _, run := range runs {
		old := run.Get("runId").Str()
		id := bundleMustString(canonical.DigestJSON(withoutKey(run.Obj(), "runId")))
		run.Obj().Set("runId", jsjson.MakeString(id))
		renamed[old] = id
	}
	rename := func(s jsjson.Value) {
		if s.Kind() != jsjson.Object {
			return
		}
		ids := s.Get("runIds")
		if ids.Kind() != jsjson.Array {
			return
		}
		out := make([]jsjson.Value, len(ids.Items()))
		for i, id := range ids.Items() {
			out[i] = id
			if n, ok := renamed[id.Str()]; ok && id.Kind() == jsjson.String {
				out[i] = jsjson.MakeString(n)
			}
		}
		s.Obj().Set("runIds", jsjson.MakeArray(out))
	}
	for _, st := range investigation.Get("stableStates").Items() {
		rename(st)
	}
	transitions := investigation.Get("transitions").Items()
	for _, tr := range transitions {
		rename(tr.Get("before"))
		rename(tr.Get("after"))
	}
	writeCanonical(investigationPath, investigation)
	runPath := func(run jsjson.Value) string {
		return "runs/" + strings.TrimPrefix(run.Get("runId").Str(), "sha256:") + ".json"
	}
	transitionPath := func(i int) string { return fmt.Sprintf("transitions/%04d.json", i) }
	for _, dir := range []string{"runs", "transitions"} {
		os.MkdirAll(filepath.Join(root, dir), 0o755)
		names, _ := nodefs.ReadDirNames(filepath.Join(root, dir))
		for _, name := range names {
			if strings.HasSuffix(name, ".json") {
				os.Remove(filepath.Join(root, dir, name))
			}
		}
	}
	var runIndex, transitionIndex []jsjson.Value
	for _, run := range runs {
		writeCanonical(filepath.Join(root, filepath.FromSlash(runPath(run))), run)
		d := jsjson.NewObj()
		d.Set("runId", run.Get("runId"))
		d.Set("path", jsjson.MakeString(runPath(run)))
		runIndex = append(runIndex, jsjson.MakeObject(d))
	}
	for i, tr := range transitions {
		writeCanonical(filepath.Join(root, filepath.FromSlash(transitionPath(i))), tr)
		d := jsjson.NewObj()
		d.Set("index", jsjson.MakeNumber(float64(i)))
		d.Set("path", jsjson.MakeString(transitionPath(i)))
		transitionIndex = append(transitionIndex, jsjson.MakeObject(d))
	}
	manifestPath := filepath.Join(root, "manifest.json")
	manifest, ok := parseObjectFile(manifestPath)
	if !ok || manifest.Field("artifacts").Kind() != jsjson.Object {
		panic("resignTurn: manifest.json is not a JSON object with artifacts")
	}
	artifacts := manifest.Field("artifacts").Obj()
	artifacts.Set("runs", jsjson.MakeArray(append([]jsjson.Value{}, runIndex...)))
	artifacts.Set("transitions", jsjson.MakeArray(append([]jsjson.Value{}, transitionIndex...)))
	manifest.Set("investigationDigest", jsjson.MakeString(bundleMustString(canonical.DigestJSON(investigation))))
	writeCanonical(manifestPath, jsjson.MakeObject(manifest))
}

func rehash(root, base string) {
	if strings.HasPrefix(base, "prevention-") {
		manifest, ok := parseObjectFile(filepath.Join(root, "manifest.json"))
		if !ok {
			return
		}
		bodyText, err := nodefs.ReadText(filepath.Join(root, "prevention.json"))
		if err != nil {
			return
		}
		body, err := jsjson.Parse(bodyText)
		if err != nil {
			return
		}
		if p := manifest.Field("prevention"); p.Kind() == jsjson.Object {
			d, err := canonical.DigestJSON(body)
			if err != nil {
				return
			}
			p.Obj().Set("digest", jsjson.MakeString(d))
		}
		unsigned := jsjson.NewObj()
		for _, k := range manifest.Keys() {
			if k != "rootDigest" {
				unsigned.Set(k, manifest.Field(k))
			}
		}
		d, err := canonical.DigestJSON(jsjson.MakeObject(unsigned))
		if err != nil {
			return
		}
		manifest.Set("rootDigest", jsjson.MakeString(d))
		writeJSON(filepath.Join(root, "manifest.json"), jsjson.MakeObject(manifest))
		return
	}
	var files []string
	for _, f := range listFiles(root, "") {
		if f != "hashes.txt" && f != "ROOT.sha256" {
			files = append(files, f)
		}
	}
	var b strings.Builder
	for i, f := range canonical.SortLocale(files) {
		if i > 0 {
			b.WriteByte('\n')
		}
		data, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		b.WriteString(canonical.SHA256HexBytes(data) + "  " + f)
	}
	hashes := b.String() + "\n"
	os.WriteFile(filepath.Join(root, "hashes.txt"), []byte(hashes), 0o644)
	os.WriteFile(filepath.Join(root, "ROOT.sha256"), []byte("sha256:"+canonical.SHA256Hex(hashes)+"\n"), 0o644)
}

// applyMutation returns false when the platform cannot create the case (symlinks on Windows).
func applyMutation(root string, c testCase) bool {
	if c.tpl == nil {
		return true
	}
	t := *c.tpl
	path := filepath.Join(root, filepath.FromSlash(c.file))
	switch t.str("op") {
	case "fifo": // a named pipe: a special file the verifiers must refuse
		if err := mkfifo(path); err != nil {
			return false
		}
	case "remove-root": // the bundle directory does not exist
		if err := os.Rename(root, root+"-gone"); err != nil {
			return false
		}
	case "root-symlink":
		real := root + "-real"
		if err := os.Rename(root, real); err != nil {
			return false
		}
		if err := os.Symlink(filepath.Base(real), root); err != nil {
			return false
		}
	case "rename":
		to := filepath.Join(root, filepath.FromSlash(t.str("to")))
		os.MkdirAll(filepath.Dir(to), 0o755)
		os.Rename(path, to)
	case "replace-nested":
		depth := int(t.fields.Field("depth").Num())
		nested := t.str("prefix") + strings.Repeat("[", depth) + strings.Repeat("]", depth) + t.str("suffix")
		b, _ := os.ReadFile(path)
		os.WriteFile(path, bytes.Replace(b, latin1(t.str("find")), latin1(nested), 1), 0o644)
	case "append-catalog-lines":
		b, _ := os.ReadFile(path)
		for i := 0; i < int(t.fields.Field("count").Num()); i++ {
			b = fmt.Appendf(b, "%s  extra/%05d.json\n", strings.Repeat("0", 64), i)
		}
		os.WriteFile(path, b, 0o644)
	case "json-edit":
		o, _ := parseObjectFile(path)
		v := jsjson.MakeObject(o)
		for _, e := range jsonEdits(t) {
			v = applyJSONEdit(v, e)
		}
		if t.fields.Field("resignLedger").Bool() {
			resignLedgerJSON(v)
		}
		writeJSON(path, v)
		if also, ok := t.fields.Get("alsoEdit"); ok {
			for _, a := range also.Items() {
				other := filepath.Join(root, filepath.FromSlash(a.Get("file").Str()))
				o, _ := parseObjectFile(other)
				w := jsjson.MakeObject(o)
				for _, e := range jsonEdits(template{a.Obj()}) {
					w = applyJSONEdit(w, e)
				}
				writeJSON(other, w)
			}
		}
		if t.fields.Field("rebindLifecycle").Bool() {
			rebindLifecycle(root, v)
		}
	case "append-bytes":
		unit, _ := hex.DecodeString(t.str("hex"))
		b, _ := os.ReadFile(path)
		b = append(b, bytes.Repeat(unit, int(t.fields.Field("count").Num()))...)
		os.WriteFile(path, b, 0o644)
	case "fake-git":
		// The tree is unchanged; caseEnv puts the fake git (a POSIX shell
		// script) first on PATH.
		return runtime.GOOS != "windows"
	case "copy-file":
		b, _ := os.ReadFile(filepath.Join(fixturesDir(), filepath.FromSlash(t.str("from"))))
		os.WriteFile(path, b, 0o644)
	case "pad-file":
		// Spaces until the file is exactly total bytes.
		b, err := os.ReadFile(path)
		total := int(t.fields.Field("total").Num())
		if err != nil || len(b) > total {
			return false
		}
		os.WriteFile(path, append(b, bytes.Repeat([]byte{0x20}, total-len(b))...), 0o644)
	case "replace-pack":
		// A new trees.pack, with the source metadata's pack digest and size
		// following it.
		b, err := os.ReadFile(filepath.Join(fixturesDir(), filepath.FromSlash(t.str("from"))))
		if err != nil {
			return false
		}
		os.WriteFile(path, b, 0o644)
		metadataPath := filepath.Join(root, "source", "trees.metadata.json")
		metadata, ok := parseObjectFile(metadataPath)
		if !ok || metadata.Field("pack").Kind() != jsjson.Object {
			return false
		}
		metadata.Field("pack").Obj().Set("digest", jsjson.MakeString("sha256:"+canonical.SHA256HexBytes(b)))
		metadata.Field("pack").Obj().Set("bytes", jsjson.MakeNumber(float64(len(b))))
		writeCanonical(metadataPath, jsjson.MakeObject(metadata))
	case "sparse-file":
		// count > 1 makes path-00000, path-00001, ...
		os.MkdirAll(filepath.Dir(path), 0o755)
		paths := []string{path}
		if n, ok := t.fields.Get("count"); ok {
			paths = nil
			for i := 0; i < int(n.Num()); i++ {
				paths = append(paths, fmt.Sprintf("%s-%05d", path, i))
			}
		}
		for _, p := range paths {
			os.WriteFile(p, nil, 0o644)
			os.Truncate(p, int64(t.fields.Field("size").Num()))
		}
	case "pad-total":
		padTotal(root, c.base, path, int64(t.fields.Field("total").Num()), t.fields.Field("declared").Bool())
	case "many-files":
		os.MkdirAll(path, 0o755)
		for i := 0; i < int(t.fields.Field("count").Num()); i++ {
			os.WriteFile(filepath.Join(path, fmt.Sprintf("%05d.txt", i)), []byte(t.str("content")), 0o644)
		}
	case "flip-byte":
		b, _ := os.ReadFile(path)
		i := 0
		if t.fields.Field("offset").Num() == -1 {
			i = len(b) - 1
		}
		b[i] ^= 0x01
		os.WriteFile(path, b, 0o644)
	case "delete-file":
		os.Remove(path)
	case "make-dir":
		os.Remove(path)
		os.Mkdir(path, 0o755)
	case "symlink":
		os.Remove(path)
		if err := os.Symlink(t.str("target"), path); err != nil {
			return false
		}
	case "add-file":
		os.MkdirAll(filepath.Dir(path), 0o755)
		content, ok := expandRepeats(t.str("content"))
		if !ok {
			return false
		}
		os.WriteFile(path, []byte(jsstr.ToUTF8(content)), 0o644)
	case "add-symlink":
		// path becomes a symbolic link to target whether or not it exists;
		// an existing file or link is replaced, a directory refused
		// (cases.ts applyMutation).
		os.MkdirAll(filepath.Dir(path), 0o755)
		if info, err := os.Lstat(path); err == nil {
			if info.IsDir() || os.Remove(path) != nil {
				return false
			}
		}
		if err := os.Symlink(t.str("target"), path); err != nil {
			return false
		}
	case "set-mtime":
		// atime and mtime, without following a symbolic link (lutimesSync).
		at, err := time.Parse("2006-01-02T15:04:05.000Z", t.str("iso"))
		if err != nil || lutimes(path, at) != nil {
			return false
		}
	case "chmod":
		// A writer case applies chmod after resetModes (prepareWriterCase),
		// which would otherwise undo it.
		mode, err := strconv.ParseUint(t.str("mode"), 8, 32)
		if err != nil || os.Chmod(path, os.FileMode(mode)) != nil {
			return false
		}
	case "replace-text":
		b, _ := os.ReadFile(path)
		os.WriteFile(path, bytes.Replace(b, latin1(t.str("find")), latin1(t.str("replace")), 1), 0o644)
	case "strip-trailing-newline":
		b, _ := os.ReadFile(path)
		os.WriteFile(path, b[:len(b)-1], 0o644)
	case "prepend-bytes":
		prefix, _ := hex.DecodeString(t.str("hex"))
		b, _ := os.ReadFile(path)
		os.WriteFile(path, append(prefix, b...), 0o644)
	case "json-add-key", "json-retype", "json-drop-first":
		o, _ := parseObjectFile(path)
		switch first := firstKey(o); t.str("op") {
		case "json-add-key":
			o.Set(t.str("key"), t.fields.Field("value"))
		case "json-retype":
			if o.Field(first).Kind() == jsjson.Number {
				o.Set(first, jsjson.MakeString("retyped"))
			} else {
				o.Set(first, jsjson.MakeNumber(12345))
			}
		default:
			o.Delete(first)
		}
		writeJSON(path, jsjson.MakeObject(o))
	}
	if t.fields.Field("resignTurn").Bool() {
		resignTurnBundle(root)
	}
	if t.fields.Field("rehash").Bool() {
		rehash(root, c.base)
	}
	return true
}

// repeatMarker is <REPEAT:c:n> in a writer input or add-file content: the
// single character c repeated n times, n at most maxRepeat (cases.ts
// expandRepeats).
var repeatMarker = regexp.MustCompile(`(?s)<REPEAT:(.):([0-9]+)>`)

const maxRepeat = 8_388_608

// expandRepeats expands every <REPEAT:c:n>; ok is false when an n is over
// maxRepeat (TS throws).
func expandRepeats(s string) (string, bool) {
	ok := true
	out := repeatMarker.ReplaceAllStringFunc(s, func(m string) string {
		sub := repeatMarker.FindStringSubmatch(m)
		n, err := strconv.Atoi(sub[2])
		if err != nil || n > maxRepeat {
			ok = false
			return m
		}
		return strings.Repeat(sub[1], n)
	})
	return out, ok
}

// caseEnv adds a fake-git case's PATH to the base environment.
func caseEnv(c testCase, env map[string]string) map[string]string {
	if c.tpl == nil || c.tpl.str("fakeGit") == "" {
		return env
	}
	out := map[string]string{}
	for k, v := range env {
		out[k] = v
	}
	dir := filepath.Join(fixturesDir(), "..", "fakegit", c.tpl.str("fakeGit"))
	if c.tpl.fields.Field("fakeGitPathOnly").Bool() {
		out["PATH"] = dir + string(os.PathListSeparator) + "/nonexistent-faultline-path"
		return out
	}
	out["PATH"] = dir + string(os.PathListSeparator) + os.Getenv("PATH")
	return out
}

func firstKey(o *jsjson.Obj) string {
	if keys := o.Keys(); len(keys) > 0 {
		return keys[0]
	}
	return ""
}

func treeDigest(root string) string {
	return treeDigestWith(root, func(_ string, b []byte) string { return canonical.SHA256HexBytes(b) })
}

// treeDigestWith is treeDigest with each regular file described by
// fileDigest (the writer replay's relocated digest; cases.ts treeDigest's
// second argument).
func treeDigestWith(root string, fileDigest func(rel string, b []byte) string) string {
	return treeDigestAliased(root, fileDigest, func(rel string) (string, bool) { return rel, true })
}

// treeDigestAliased is treeDigestWith with each path named by alias (false
// leaves the entry out). A renamed line goes last, sorted, since its order
// would follow the object ids; an unchanged path keeps its place
// (cases.ts treeDigest).
func treeDigestAliased(root string, fileDigest func(rel string, b []byte) string, alias func(rel string) (string, bool)) string {
	var lines, moved []string
	var walk func(rel string)
	walk = func(rel string) {
		names, _ := nodefs.ReadDirNames(filepath.Join(root, filepath.FromSlash(rel)))
		for _, name := range names {
			r := name
			if rel != "" {
				r = rel + "/" + name
			}
			path := filepath.Join(root, filepath.FromSlash(r))
			info, _ := os.Lstat(path)
			shown, keep := alias(r)
			add := func(kind, rest string) {
				switch {
				case !keep:
				case shown == r:
					lines = append(lines, kind+" "+shown+rest)
				default:
					moved = append(moved, kind+" "+shown+rest)
				}
			}
			switch {
			case info.Mode()&os.ModeSymlink != 0:
				// Windows reports the link target with backslashes; the tree
				// digest records the logical target as TS wrote it.
				target, _ := os.Readlink(path)
				add("L", " "+filepath.ToSlash(target))
			case info.IsDir():
				add("D", "")
				walk(r)
			case !info.Mode().IsRegular(): // a FIFO: reading it would block
				add("S", "")
			default:
				b, _ := os.ReadFile(path)
				add("F", " "+fileDigest(r, b))
			}
		}
	}
	walk("")
	sort.Strings(moved)
	return canonical.SHA256Hex(strings.Join(append(lines, moved...), "\n"))
}

var gitLabels = []string{
	"Git bundle head listing", "Temporary Git verifier initialization", "Git bundle verification",
	"Git bundle extraction", "Git commit resolution", "Git tree resolution", "Git bundle range enumeration",
	"Git binary range patch creation", "Git bundle object-format resolution",
}

// turnGitLabels precede git's stderr in the turn verifier's and writer's
// messages (turn-proof-bundle.ts:417, 428, 457, 475), in the ": " form;
// turnEnumerateLabel is the one that names a tree.
var (
	turnGitLabels = []string{
		"Turn tree pack is not a valid Git pack: ",
		"Could not create temporary bare repository for pack verification: ",
		"Could not pack turn trees: ",
	}
	turnEnumerateLabel = regexp.MustCompile(`Could not enumerate objects for turn tree [0-9a-f]{40,64}: `)
)

// gitTmpMarkers name the git verifiers' temporary directories.
var gitTmpMarkers = []string{"faultline-git-proof-verify-", "faultline-turn-proof-pack-"}

func isBoundary(c byte) bool {
	return c == '\'' || c == '"' || c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

func isAlnum(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// maskGitDetail hides git's own diagnostics (they vary across git
// versions): each run of lines starting with "error: ", "fatal: ",
// "warning: " or "hint: " becomes one <GIT-STDERR> (the number of such
// lines varies across git versions; a line containing "; " is not masked).
// Everything else stays:
// other git output, the text FaultLine composes (the "; " join with Node's
// spawnSync error, the "exit <status>" fallback), and anything a port adds.
func maskGitDetail(detail string) string {
	if status, ok := strings.CutPrefix(detail, "exit "); ok {
		if _, err := strconv.Atoi(status); err == nil || status == "null" {
			return detail
		}
	}
	body, tail := detail, ""
	if m := spawnSuffix.FindStringSubmatch(detail); m != nil && (m[1] == "") == !strings.HasPrefix(m[2], "; ") {
		body, tail = m[1], m[2]
	}
	var out []string
	for _, line := range strings.Split(body, "\n") {
		switch {
		case !gitDiagnostic.MatchString(line) || strings.Contains(line, "; "):
			// A "; " on a diagnostic line is FaultLine's own join (an exit
			// status or spawn error glued onto git's text): keep it visible.
			out = append(out, line)
		case len(out) == 0 || out[len(out)-1] != "<GIT-STDERR>":
			out = append(out, "<GIT-STDERR>")
		}
	}
	return strings.Join(out, "\n") + tail
}

var (
	spawnSuffix   = regexp.MustCompile(`^((?s:.*?))((?:; )?spawnSync git [A-Z0-9_]+)$`)
	gitDiagnostic = regexp.MustCompile(`^(?:error|fatal|warning|hint): `)
)

func normalize(text, bundle string) string {
	out, _ := normalizeChecked(text, bundle, true)
	return out
}

// maskGit is false for fake-git cases: the fake gits print fixed text, so
// their git detail is compared in full (lines FaultLine adds or drops
// show).
func maskGit(c testCase) bool { return c.tpl == nil || c.tpl.str("fakeGit") == "" }

// normalizeChecked masks run-specific text. On Windows it also rewrites the
// separators after <BUNDLE> to "/" to compare with Linux goldens, and reports
// whether any "/" was already there: Node's path.win32 joins with "\", so a
// "/" means Go built the path differently from TS on Windows.
func normalizeChecked(text, bundle string, maskGit bool) (string, bool) {
	out := text
	// Only the path passed to fl is masked, so output that names a resolved
	// (physical) path instead still differs. Windows temp directories can be
	// 8.3 short names that git expands, so there the long form is masked too.
	for _, p := range maskedForms(bundle, false) {
		out = strings.ReplaceAll(out, p, "<BUNDLE>")
	}
	out = maskGitOutput(out, maskGit)
	forwardSlash := false
	if runtime.GOOS == "windows" {
		out, forwardSlash = slashesAfter(out, "<BUNDLE>", false)
	}
	return out, forwardSlash
}

// maskedForms are the spellings of path that output can carry: the path
// itself and, on Windows, its long (symlink-free) form; with jsonEscaped,
// also each one as JSON.stringify escapes it (a Windows "\" doubles).
func maskedForms(path string, jsonEscaped bool) []string {
	paths := []string{path}
	if runtime.GOOS == "windows" {
		if real, err := filepath.EvalSymlinks(path); err == nil && real != path {
			paths = append(paths, real)
		}
	}
	if jsonEscaped {
		for _, p := range paths {
			if escaped := strings.ReplaceAll(p, `\`, `\\`); escaped != p {
				paths = append(paths, escaped)
			}
		}
	}
	return paths
}

// writerRoots are the directories a writer case owns (makeWriterRoots in
// writer_test.go); normalizeWriter masks them.
type writerRoots struct{ root, ws, home, tmp, argvLog, recorder string }

// normalizeWriter masks a writer case's own directories (slice 2 spec 2.3):
// the workspace, its TMPDIR and HOME, a judge-proof server port, then the
// git masks verify uses. Digests are never masked. On Windows the
// separators after each mask become "/" (JSON-escaped ones included), to
// compare with goldens generated on Linux.
func normalizeWriter(text string, roots writerRoots) string {
	out := text
	masks := [][2]string{{roots.ws, "<WS>"}, {roots.tmp, "<TMP>"}, {roots.home, "<HOME>"}}
	for _, m := range masks {
		for _, p := range maskedForms(m[0], true) {
			out = strings.ReplaceAll(out, p, m[1])
		}
	}
	// A random port; judge-proof's fixed default 4174 stays (gen/cases.ts).
	out = loopbackPort.ReplaceAllStringFunc(out, func(s string) string {
		if s == "127.0.0.1:4174" {
			return s
		}
		return "127.0.0.1:<PORT>"
	})
	out = stagedPID.ReplaceAllString(out, ".<pid>.$1.tmp")
	out = stageKey.ReplaceAllString(out, ".<STAGEKEY>.<pid>.tmp")
	out = lockAge.ReplaceAllString(out, `${1}"<LOCKAGE>"`)
	out = maskGitOutput(out, true)
	if runtime.GOOS == "windows" {
		for _, m := range masks {
			out, _ = slashesAfter(out, m[1], true)
		}
	}
	return out
}

var loopbackPort = regexp.MustCompile(`127\.0\.0\.1:[0-9]+`)

// stagedPID masks the process id in an atomic write's staged name
// (.<name>.<pid>.<uuid>.tmp), which a failed rename prints.
var stagedPID = regexp.MustCompile(`\.[0-9]+\.([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\.tmp`)

// stageKey masks a sidecar receipt stage (.<sha256(receipt path)[0:24]>.<pid>.tmp,
// codex-sidecar.ts writeJsonAtomic): its key hashes the absolute receipt
// path, its pid is the process's.
var stageKey = regexp.MustCompile(`\.([0-9a-f]{24})\.([0-9]+)\.tmp`)

// lockAge masks sidecar status's ACTIVE lock.ageMilliseconds
// (codex-sidecar.ts sidecarLockHealth), which a seed cannot fix. A STALE age
// is seeded by set-mtime and stays compared.
var lockAge = regexp.MustCompile(`("state": "ACTIVE",\s*"ageMilliseconds": )[0-9]+`)

// maskGitOutput replaces git's temporary verifier directories with <GITTMP>
// and, with maskGit, git's own diagnostics after each gitLabels failure.
func maskGitOutput(text string, maskGit bool) string {
	out := text
	for _, marker := range gitTmpMarkers {
		for i := strings.Index(out, marker); i >= 0; {
			start := i
			for start > 0 && !isBoundary(out[start-1]) {
				start--
			}
			end := i + len(marker)
			for end < len(out) && isAlnum(out[end]) {
				end++
			}
			out = out[:start] + "<GITTMP>" + out[end:]
			next := strings.Index(out[start+len("<GITTMP>"):], marker)
			if next < 0 {
				break
			}
			i = start + len("<GITTMP>") + next
		}
	}
	if !maskGit {
		return out
	}
	// Each needle finds the start of the next detail at or after from (-1 if none).
	var needles []func(from int) int
	literal := func(needle string) func(int) int {
		return func(from int) int {
			if i := strings.Index(out[from:], needle); i >= 0 {
				return from + i + len(needle)
			}
			return -1
		}
	}
	for _, label := range gitLabels {
		needles = append(needles, literal(label+" failed: "))
	}
	for _, label := range turnGitLabels {
		needles = append(needles, literal(label))
	}
	needles = append(needles, func(from int) int {
		if m := turnEnumerateLabel.FindStringIndex(out[from:]); m != nil {
			return from + m[1]
		}
		return -1
	})
	for _, after := range needles {
		for from := after(0); from >= 0; from = after(from) {
			to := strings.Index(out[from:], "\n- ")
			if to >= 0 {
				to += from
			} else if strings.HasSuffix(out, "\n") {
				to = len(out) - 1
			} else {
				to = len(out)
			}
			detail := maskGitDetail(out[from:to])
			out = out[:from] + detail + out[to:]
			from += len(detail)
		}
	}
	return out
}

// slashesAfter rewrites the Windows separators in the path that follows each
// mask, up to a boundary, to "/"; with jsonEscaped, a "\\" (an escaped
// separator) counts as one. It reports whether a "/" was already there.
func slashesAfter(text, mask string, jsonEscaped bool) (string, bool) {
	var b strings.Builder
	forwardSlash := false
	out := text
	for {
		i := strings.Index(out, mask)
		if i < 0 {
			b.WriteString(out)
			break
		}
		b.WriteString(out[:i+len(mask)])
		rest := out[i+len(mask):]
		end := 0
		for end < len(rest) && !isBoundary(rest[end]) {
			end++
		}
		path := rest[:end]
		forwardSlash = forwardSlash || strings.Contains(path, "/")
		if jsonEscaped {
			path = strings.ReplaceAll(path, `\\`, "/")
		}
		b.WriteString(strings.ReplaceAll(path, `\`, "/"))
		out = rest[end:]
	}
	return b.String(), forwardSlash
}

func baseRoot(root, base string) string {
	if strings.HasPrefix(base, "prevention-") {
		if o, ok := parseObjectFile(filepath.Join(root, "manifest.json")); ok {
			return o.Field("rootDigest").Str()
		}
		return "missing"
	}
	text, err := nodefs.ReadText(filepath.Join(root, "ROOT.sha256"))
	if err != nil {
		return "missing"
	}
	return jsstr.Trim(text)
}
