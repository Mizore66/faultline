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
	"strconv"
	"strings"

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
var pathOps = map[string]bool{"add-file": true, "root-symlink": true, "sparse-file": true, "many-files": true, "fake-git": true, "pad-total": true}

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
		if bases, ok := t.fields.Get("bases"); ok && !slices.ContainsFunc(bases.Items(), func(b jsjson.Value) bool { return b.Str() == base }) {
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
func padTotal(root, base, path string, total int64) {
	const chunk = 120 << 20
	size := func() int64 {
		var n int64
		for _, f := range listFiles(root, "") {
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

// rebindLifecycle is the TS engine's rebindLifecycle: manifest.lifecycle
// bound to the edited ledger as the writer's bindLifecycleLedger does.
func rebindLifecycle(root string, ledger jsjson.Value) {
	investigation, _ := parseObjectFile(filepath.Join(root, "investigation.json"))
	manifestPath := filepath.Join(root, "manifest.json")
	manifest, _ := parseObjectFile(manifestPath)
	stateByCommit := map[string]jsjson.Value{}
	states := investigation.Field("states").Items()
	for _, st := range states {
		stateByCommit[st.Get("commit").Str()] = st // new Map(...): the last one wins
	}
	var bindings []jsjson.Value
	covered := map[float64]bool{}
	for _, event := range ledger.Get("events").Items() {
		cp := event.Get("event", "payload", "checkpoint")
		if event.Get("event", "type").Str() != "WORKTREE_CHECKPOINT" || cp.Kind() != jsjson.Object {
			continue
		}
		state, ok := stateByCommit[cp.Get("headCommit").Str()]
		if !ok {
			continue
		}
		b := jsjson.NewObj()
		b.Set("sequence", event.Get("sequence"))
		b.Set("stateIndex", state.Get("index"))
		b.Set("checkpointDigest", cp.Get("digest"))
		bindings = append(bindings, jsjson.MakeObject(b))
		covered[state.Get("index").Num()] = true
	}
	status := "PARTIALLY_BOUND"
	if len(covered) == len(states) {
		status = "FULLY_BOUND"
	}
	events := ledger.Get("events").Items()
	lifecycle := jsjson.NewObj()
	for _, k := range manifest.Field("lifecycle").Obj().Keys() {
		lifecycle.Set(k, manifest.Field("lifecycle").Get(k))
	}
	lifecycle.Set("status", jsjson.MakeString(status))
	lifecycle.Set("ledgerDigest", jsjson.MakeString(bundleMustString(canonical.DigestJSON(ledger))))
	lifecycle.Set("headHash", events[len(events)-1].Get("hash"))
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
		padTotal(root, c.base, path, int64(t.fields.Field("total").Num()))
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
		os.WriteFile(path, []byte(t.str("content")), 0o644)
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
	if t.fields.Field("rehash").Bool() {
		rehash(root, c.base)
	}
	return true
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
	var lines []string
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
			switch {
			case info.Mode()&os.ModeSymlink != 0:
				// Windows reports the link target with backslashes; the tree
				// digest records the logical target as TS wrote it.
				target, _ := os.Readlink(path)
				lines = append(lines, "L "+r+" "+filepath.ToSlash(target))
			case info.IsDir():
				lines = append(lines, "D "+r)
				walk(r)
			default:
				b, _ := os.ReadFile(path)
				lines = append(lines, "F "+r+" "+canonical.SHA256HexBytes(b))
			}
		}
	}
	walk("")
	return canonical.SHA256Hex(strings.Join(lines, "\n"))
}

var gitLabels = []string{
	"Git bundle head listing", "Temporary Git verifier initialization", "Git bundle verification",
	"Git bundle extraction", "Git commit resolution", "Git tree resolution", "Git bundle range enumeration",
	"Git binary range patch creation", "Git bundle object-format resolution",
}

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
	paths := []string{bundle}
	if runtime.GOOS == "windows" {
		if real, err := filepath.EvalSymlinks(bundle); err == nil && real != bundle {
			paths = append(paths, real)
		}
	}
	for _, p := range paths {
		out = strings.ReplaceAll(out, p, "<BUNDLE>")
	}
	const marker = "faultline-git-proof-verify-"
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
	labels := gitLabels
	if !maskGit {
		labels = nil
	}
	for _, label := range labels {
		needle := label + " failed: "
		for i := strings.Index(out, needle); i >= 0; {
			from := i + len(needle)
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
			next := strings.Index(out[from+len(detail):], needle)
			if next < 0 {
				break
			}
			i = from + len(detail) + next
		}
	}
	forwardSlash := false
	if runtime.GOOS == "windows" {
		var b strings.Builder
		for {
			i := strings.Index(out, "<BUNDLE>")
			if i < 0 {
				b.WriteString(out)
				break
			}
			b.WriteString(out[:i+len("<BUNDLE>")])
			rest := out[i+len("<BUNDLE>"):]
			end := 0
			for end < len(rest) && !isBoundary(rest[end]) {
				end++
			}
			forwardSlash = forwardSlash || strings.Contains(rest[:end], "/")
			b.WriteString(strings.ReplaceAll(rest[:end], `\`, "/"))
			out = rest[end:]
		}
		out = b.String()
	}
	return out, forwardSlash
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
