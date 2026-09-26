package difftest

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
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
		parent.Items()[int(k.Num())] = value // shares backing storage with the tree
	} else {
		parent.Obj().Set(k.Str(), value)
	}
}

func jsonEditApplicable(t template, path string) bool {
	o, ok := parseObjectFile(path)
	if !ok {
		return false
	}
	v := jsjson.MakeObject(o)
	p := t.jsonPath("jsonPath")
	if parent, ok := at(v, p[:len(p)-1]); !ok || (parent.Kind() != jsjson.Object && parent.Kind() != jsjson.Array) {
		return false
	}
	has := func(k string) bool { _, ok := t.fields.Get(k); return ok }
	if has("delete") || has("valueFrom") || has("swapWith") {
		if _, ok := at(v, p); !ok {
			return false
		}
	}
	for _, k := range []string{"valueFrom", "swapWith"} {
		if has(k) {
			if _, ok := at(v, t.jsonPath(k)); !ok {
				return false
			}
		}
	}
	return true
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

// pathOps target a path in the bundle (or the bundle itself), not an existing file.
var pathOps = map[string]bool{"add-file": true, "root-symlink": true}

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
		p := t.jsonPath("jsonPath")
		_, del := t.fields.Get("delete")
		_, swap := t.fields.Get("swapWith")
		_, from := t.fields.Get("valueFrom")
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
			b, _ := at(v, t.jsonPath("swapWith"))
			setAt(v, p, b)
			setAt(v, t.jsonPath("swapWith"), a)
		case from:
			src, _ := at(v, t.jsonPath("valueFrom"))
			setAt(v, p, src)
		default:
			setAt(v, p, t.fields.Field("value"))
		}
		writeJSON(path, v)
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

// maskGitDetail hides git's own stderr (it varies across git versions) but
// keeps the text FaultLine composes around it: the "; " join with Node's
// spawnSync error, and the "exit <status>" fallback.
func maskGitDetail(detail string) string {
	if status, ok := strings.CutPrefix(detail, "exit "); ok {
		if _, err := strconv.Atoi(status); err == nil || status == "null" {
			return detail
		}
	}
	if i := strings.LastIndex(detail, "spawnSync git "); i >= 0 {
		code := detail[i+len("spawnSync git "):]
		if code != "" && strings.Trim(code, "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_") == "" {
			if i == 0 {
				return detail
			}
			if strings.HasSuffix(detail[:i], "; ") {
				return "<GIT-STDERR>; " + detail[i:]
			}
		}
	}
	return "<GIT-STDERR>"
}

func normalize(text, bundle string) string {
	out, _ := normalizeChecked(text, bundle)
	return out
}

// normalizeChecked masks run-specific text. On Windows it also rewrites the
// separators after <BUNDLE> to "/" to compare with Linux goldens, and reports
// whether any "/" was already there: Node's path.win32 joins with "\", so a
// "/" means Go built the path differently from TS on Windows.
func normalizeChecked(text, bundle string) (string, bool) {
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
	for _, label := range gitLabels {
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
