//go:build faultline_test

package difftest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
)

// Frozen-TypeScript oracle for `fl witness propose --live` (spec 2026-11-13,
// Task 5). Every case is run twice from independently generated temp roots:
// the frozen dist/cli.js on Node v22.22.2 (through the same seed preload and
// fetch shim as the writer goldens) and the built Go fl, each against its
// own scripted loopback OpenAI server. Exact stdout, stderr, exit status,
// every request the provider saw (method, target, Authorization,
// Content-Type, body), the request count and the whole workspace tree (path,
// file mode and content digest) must match. Only the actual ephemeral port
// and the independently generated temp root are normalized. The approved
// deviation is NOT masked: for default-model cases the test asserts the
// frozen side sent "model":"gpt-5.6" and the Go side "gpt-6.1", and that the
// frozen output printed "source": "GPT-5.6" where Go printed "GPT-6.1"; it
// then applies exactly those two replacements to the frozen text and
// compares everything else byte for byte.
//
// FAULTLINE_ORACLE_NODE names the pinned Node v22.22.2 binary (default
// `node`); a different version fails. The test is skipped unless
// FAULTLINE_NODE_ORACLE=1.

type wloReply struct {
	status int
	ctype  string
	body   string
	drop   bool // close the connection without answering
}

type wloCase struct {
	name         string
	incident     string // incident.json text; "" means the standard incident
	noIncident   bool
	files        map[string]string // workspace-relative path -> content
	symlinks     map[string]string // workspace-relative link -> literal target
	noOverlayDir bool
	args         []string // options after "witness propose --live"; nil means the standard options
	noKey        bool
	reply        wloReply // "@@WS@@" in body is the side's own workspace path
	defaultModel bool     // frozen sends gpt-5.6 and prints GPT-5.6; Go gpt-6.1 and GPT-6.1
	second       [][]string
}

const wloIncident = `{"symptom":"refund fails","ciLog":"Expected USD; received EUR.","repositoryLanguage":"JavaScript","repositorySummary":"payments"}`

var wloStdArgs = []string{"--incident", "incident.json", "--proposal-id", "live-1", "--overlay-root", "overlays"}

func wloProposal(overlays string) string {
	return `{"behavior":"Preserve refund currency.","command":"node witness.mjs","overlayFiles":` + overlays + `,"expectedExitCode":0,"timeoutSeconds":30,"safetyNotes":["n"]}`
}

func wloQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r < 0x20:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func wloRaw(proposal string) wloReply {
	return wloReply{200, "application/json", `{"output":[{"type":"message","content":[{"type":"output_text","text":` + wloQuote(proposal) + `}]}]}`, false}
}
func wloFast(proposal string) wloReply {
	return wloReply{200, "application/json", `{"output_text":` + wloQuote(proposal) + `}`, false}
}
func wloBoth(proposal string) wloReply {
	q := wloQuote(proposal)
	return wloReply{200, "application/json", `{"output_text":` + q + `,"output":[{"type":"message","content":[{"type":"output_text","text":` + q + `}]}]}`, false}
}
func wloJSON(body string) wloReply { return wloReply{200, "application/json", body, false} }

func wloCases() []wloCase {
	std := func(name, overlays string, files map[string]string) wloCase {
		return wloCase{name: name, files: files, reply: wloRaw(wloProposal(overlays))}
	}
	a := map[string]string{"overlays/a.txt": "alpha\n"}
	big := func(n int) string { return strings.Repeat("a", n) }
	rawBadSchema := func(name, text string) wloCase { return wloCase{name: name, reply: wloRaw(text)} }
	return []wloCase{
		{name: "success raw custom model", files: map[string]string{"overlays/a.txt": "alpha\r\nbeta\n"}, args: append(slices.Clone(wloStdArgs), "--model", "gpt-test"), reply: wloRaw(wloProposal(`["a.txt"]`))},
		{name: "success fast path default model", files: map[string]string{"overlays/a.txt": "a", "overlays/z/w.bin": "\x00\xff\x01"}, reply: wloFast(wloProposal(`["z/w.bin","./a.txt"]`)), defaultModel: true},
		{name: "success raw and fast path agree", files: a, reply: wloBoth(wloProposal(`["a.txt"]`)), defaultModel: true},
		{name: "success no overlays", reply: wloRaw(wloProposal(`[]`)), defaultModel: true},
		{name: "success empty model", args: append(slices.Clone(wloStdArgs), "--model", ""), reply: wloRaw(wloProposal(`[]`))},
		{name: "success five overlays", files: map[string]string{"overlays/1": "1", "overlays/2": "2", "overlays/3": "3", "overlays/4": "4", "overlays/5": "5"}, reply: wloRaw(wloProposal(`["5","4","3","2","1"]`)), defaultModel: true},
		{name: "success redacted incident", incident: `{"symptom":"leak sk-proj-abcdefghijklmnop12345678 and token=abcdef0123456789","ciLog":"Authorization: Bearer abcdefghijklmnop1234 é😀","repositoryLanguage":"JavaScript","repositorySummary":"payments"}`, reply: wloRaw(wloProposal(`[]`)), defaultModel: true},
		{name: "success proposal extra fields", reply: wloRaw(`{"behavior":"b","command":"c","overlayFiles":[],"expectedExitCode":0,"timeoutSeconds":5,"safetyNotes":[],"extra":1}`), defaultModel: true},
		{name: "storage limit overlay 1050000", files: map[string]string{"overlays/big": big(1_050_000)}, reply: wloRaw(wloProposal(`["big"]`))},
		{name: "storage limit overlay 1050001", files: map[string]string{"overlays/big": big(1_050_001)}, reply: wloRaw(wloProposal(`["big"]`))},
		{name: "storage limit overlay 1200000", files: map[string]string{"overlays/big": big(1_200_000)}, reply: wloRaw(wloProposal(`["big"]`))},

		{name: "http 400", reply: wloReply{400, "application/json", `{"error":"bad"}`, false}},
		{name: "http 429 text", reply: wloReply{429, "text/plain", "slow down", false}},
		{name: "http 500 empty", reply: wloReply{500, "", "", false}},
		{name: "http 302 no location", reply: wloReply{302, "text/plain", "moved", false}},
		{name: "http 204", reply: wloReply{204, "", "", false}},
		{name: "malformed response", reply: wloJSON(`{"output":`)},
		{name: "empty 200 body", reply: wloJSON(``)},
		{name: "non-json 200 body", reply: wloReply{200, "text/plain", "hello", false}},
		{name: "response array", reply: wloJSON(`[]`)},
		{name: "response null", reply: wloJSON(`null`)},
		{name: "no output text", reply: wloJSON(`{"output":[]}`)},
		{name: "output not array", reply: wloJSON(`{"output":{}}`)},
		{name: "output non-object item", reply: wloJSON(`{"output":[1]}`)},
		{name: "content not array", reply: wloJSON(`{"output":[{"content":"x"}]}`)},
		{name: "content non-object", reply: wloJSON(`{"output":[{"content":[null]}]}`)},
		{name: "empty output text", reply: wloJSON(`{"output":[{"content":[{"type":"output_text","text":""}]}]}`)},
		{name: "multiple output text", reply: wloJSON(`{"output":[{"content":[{"type":"output_text","text":"{}"},{"type":"output_text","text":"{}"}]}]}`)},
		{name: "fast path disagrees", reply: wloJSON(`{"output_text":"{}","output":[{"content":[{"type":"output_text","text":` + wloQuote(wloProposal(`[]`)) + `}]}]}`)},
		{name: "fast path empty", reply: wloJSON(`{"output_text":""}`)},
		{name: "fast path non-string", reply: wloJSON(`{"output_text":3}`)},
		rawBadSchema("text not json", `not json`),
		rawBadSchema("text empty object", `{}`),
		rawBadSchema("schema missing field", `{"behavior":"b","command":"c","overlayFiles":[],"expectedExitCode":0,"timeoutSeconds":5}`),
		rawBadSchema("schema timeout zero", `{"behavior":"b","command":"c","overlayFiles":[],"expectedExitCode":0,"timeoutSeconds":0,"safetyNotes":[]}`),
		rawBadSchema("schema timeout 121", `{"behavior":"b","command":"c","overlayFiles":[],"expectedExitCode":0,"timeoutSeconds":121,"safetyNotes":[]}`),
		rawBadSchema("schema exit code fractional", `{"behavior":"b","command":"c","overlayFiles":[],"expectedExitCode":1.5,"timeoutSeconds":5,"safetyNotes":[]}`),
		rawBadSchema("schema exit code 256", `{"behavior":"b","command":"c","overlayFiles":[],"expectedExitCode":256,"timeoutSeconds":5,"safetyNotes":[]}`),
		std("schema six overlays", `["1","1","1","1","1","1"]`, map[string]string{"overlays/1": "1"}),
		rawBadSchema("schema nine safety notes", `{"behavior":"b","command":"c","overlayFiles":[],"expectedExitCode":0,"timeoutSeconds":5,"safetyNotes":["1","2","3","4","5","6","7","8","9"]}`),
		std("schema overlay not string", `[1]`, nil),
		rawBadSchema("schema wrong types", `{"behavior":1,"command":2,"overlayFiles":"x","expectedExitCode":"0","timeoutSeconds":"5","safetyNotes":{}}`),
		rawBadSchema("schema empty behavior", `{"behavior":"","command":"c","overlayFiles":[],"expectedExitCode":0,"timeoutSeconds":5,"safetyNotes":[]}`),
		{name: "transport connection dropped", reply: wloReply{drop: true}},

		{name: "no key", noKey: true, reply: wloRaw(wloProposal(`[]`))},
		{name: "missing incident file", noIncident: true},
		{name: "invalid incident json", incident: `{`},
		{name: "incident extra field", incident: `{"symptom":"s","ciLog":"c","repositoryLanguage":"l","repositorySummary":"r","x":1}`},
		{name: "incident missing field", incident: `{"symptom":"s"}`},
		{name: "incident empty field", incident: `{"symptom":"","ciLog":"c","repositoryLanguage":"l","repositorySummary":"r"}`},
		{name: "missing proposal id", args: []string{"--incident", "incident.json", "--overlay-root", "overlays"}, reply: wloRaw(wloProposal(`[]`))},
		{name: "missing overlay root", args: []string{"--incident", "incident.json", "--proposal-id", "live-1"}, reply: wloRaw(wloProposal(`[]`))},
		{name: "bad proposal id", args: []string{"--incident", "incident.json", "--proposal-id", "../x", "--overlay-root", "overlays"}, reply: wloRaw(wloProposal(`[]`))},
		{name: "empty proposal id", args: []string{"--incident", "incident.json", "--proposal-id", "", "--overlay-root", "overlays"}, reply: wloRaw(wloProposal(`[]`))},

		{name: "root missing", noOverlayDir: true, reply: wloRaw(wloProposal(`[]`))},
		{name: "root is file", noOverlayDir: true, files: map[string]string{"overlays": "file"}, reply: wloRaw(wloProposal(`[]`))},
		{name: "root is symlink", noOverlayDir: true, files: map[string]string{"real/a.txt": "a"}, symlinks: map[string]string{"overlays": "real"}, reply: wloRaw(wloProposal(`["a.txt"]`))},
		{name: "root symlink ancestor", noOverlayDir: true, files: map[string]string{"real/o/a.txt": "a"}, symlinks: map[string]string{"link": "real"}, args: []string{"--incident", "incident.json", "--proposal-id", "live-1", "--overlay-root", "link/o"}, reply: wloRaw(wloProposal(`["a.txt"]`))},
		{name: "root relative dots", files: map[string]string{"overlays/a.txt": "a"}, args: []string{"--incident", "incident.json", "--proposal-id", "live-1", "--overlay-root", "./overlays/../overlays"}, reply: wloRaw(wloProposal(`["a.txt"]`))},
		std("path parent traversal", `["../x"]`, map[string]string{"x": "x"}),
		std("path nested traversal", `["a/../../x"]`, a),
		std("path inner dotdot", `["d/../a.txt"]`, map[string]string{"overlays/a.txt": "a", "overlays/d/x": "x"}),
		std("path dot", `["."]`, a),
		std("path empty", `[""]`, a),
		std("path backslash", `["a\\b"]`, a),
		std("path absolute outside", `["/etc/hosts"]`, a),
		std("path absolute inside root", `["@@WS@@/overlays/a.txt"]`, a),
		std("path trailing slash", `["a.txt/"]`, a),
		std("path double slash", `["d//x"]`, map[string]string{"overlays/d/x": "x"}),
		std("path leading dot slash", `["./d/x"]`, map[string]string{"overlays/d/x": "x"}),
		std("path nul", `["a\u0000b"]`, a),
		std("path space name", `[" a "]`, map[string]string{"overlays/ a ": "x"}),
		std("path unicode", `["é/😀.txt"]`, map[string]string{"overlays/é/😀.txt": "x"}),
		std("path long 513", `["`+strings.Repeat("a", 513)+`"]`, nil),
		std("path long 512 missing", `["`+strings.Repeat("a", 512)+`"]`, nil),
		std("path missing leaf", `["nope.txt"]`, a),
		std("path missing parent", `["nope/a.txt"]`, a),
		std("path parent is file", `["a.txt/b"]`, a),
		std("path leaf is directory", `["d"]`, map[string]string{"overlays/d/x": "x"}),
		{name: "path leaf is symlink", files: map[string]string{"overlays/real": "r"}, symlinks: map[string]string{"overlays/l": "real"}, reply: wloRaw(wloProposal(`["l"]`))},
		{name: "path parent is symlink", files: map[string]string{"overlays/real/x": "r"}, symlinks: map[string]string{"overlays/l": "real"}, reply: wloRaw(wloProposal(`["l/x"]`))},
		{name: "path dangling symlink", symlinks: map[string]string{"overlays/l": "gone"}, reply: wloRaw(wloProposal(`["l"]`))},
		{name: "path symlink escaping", files: map[string]string{"outside": "o"}, symlinks: map[string]string{"overlays/l": "../outside"}, reply: wloRaw(wloProposal(`["l"]`))},
		std("path empty file", `["e"]`, map[string]string{"overlays/e": ""}),
		std("path duplicate exact", `["a.txt","a.txt"]`, a),
		std("path duplicate dot slash", `["a.txt","./a.txt"]`, a),
		std("path case alias missing", `["a.txt","A.TXT"]`, a),
		std("path case alias both present", `["a.txt","A.txt"]`, map[string]string{"overlays/a.txt": "a"}),
		std("path second overlay fails", `["a.txt","missing"]`, a),
		std("path first overlay fails", `["missing","a.txt"]`, a),
		std("path sorting", `["b","a","c/d"]`, map[string]string{"overlays/a": "a", "overlays/b": "b", "overlays/c/d": "d"}),
		std("path sorting case mix", `["B","a","_","Z"]`, map[string]string{"overlays/B": "b", "overlays/a": "a", "overlays/_": "_", "overlays/Z": "z"}),

		{name: "second invocation refusal", files: a, reply: wloRaw(wloProposal(`["a.txt"]`)), second: [][]string{slices.Clone(wloStdArgs)}, defaultModel: true},
		{name: "second invocation other id", files: a, reply: wloRaw(wloProposal(`["a.txt"]`)), second: [][]string{{"--incident", "incident.json", "--proposal-id", "live-2", "--overlay-root", "overlays"}}},
		{name: "second invocation after failure", files: a, reply: wloRaw(wloProposal(`["a.txt","missing"]`)), second: [][]string{slices.Clone(wloStdArgs)}},
	}
}

type wloObservation struct {
	stdout, stderr string
	code           int
	requests       []string
}

type wloProvider struct {
	mu      sync.Mutex
	records []string
	srv     *http.Server
	url     string
}

func startWloProvider(t *testing.T, reply wloReply) *wloProvider {
	t.Helper()
	p := &wloProvider{}
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p.srv = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		p.mu.Lock()
		p.records = append(p.records, strings.Join([]string{r.Method, r.RequestURI, r.Header.Get("Authorization"), r.Header.Get("Content-Type"), string(body)}, "\x1f"))
		p.mu.Unlock()
		if reply.drop {
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				conn.Close()
			}
			return
		}
		out := []byte(reply.body)
		h := w.Header()
		h["Date"] = nil
		h.Set("Content-Length", fmt.Sprint(len(out)))
		if reply.ctype != "" {
			h.Set("Content-Type", reply.ctype)
		} else {
			h["Content-Type"] = nil
		}
		w.WriteHeader(reply.status)
		if reply.status != 204 {
			w.Write(out)
		}
	})}
	go p.srv.Serve(l)
	p.url = "http://" + l.Addr().String() + "/v1/responses"
	t.Cleanup(func() { p.srv.Close() })
	return p
}

func (p *wloProvider) take(from int) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.records[from:])
}

func (p *wloProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.records)
}

// wloSide is one independent execution environment.
type wloSide struct {
	root, ws string
	provider *wloProvider
	cmd      func(args []string) *exec.Cmd
}

func wloRoots(t *testing.T) (root, ws, home, tmp string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws, home, tmp = filepath.Join(root, "ws"), filepath.Join(root, "home"), filepath.Join(root, "tmp")
	for _, d := range []string{ws, home, tmp} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return
}

func buildWloWorkspace(t *testing.T, ws string, c wloCase) {
	t.Helper()
	write := func(rel, content string) {
		p := filepath.Join(ws, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if !c.noIncident {
		text := c.incident
		if text == "" {
			text = wloIncident
		}
		write("incident.json", text)
	}
	if !c.noOverlayDir {
		if err := os.Mkdir(filepath.Join(ws, "overlays"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range sortedWloKeys(c.files) {
		write(rel, c.files[rel])
	}
	for _, rel := range sortedWloKeys(c.symlinks) {
		p := filepath.Join(ws, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(c.symlinks[rel], p); err != nil {
			t.Fatal(err)
		}
	}
}

func sortedWloKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func wloEnv(home, tmp, url string, noKey bool) []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "TMPDIR=" + tmp, "TEMP=" + tmp, "TMP=" + tmp,
		"LC_ALL=C.UTF-8", "LANGUAGE=", "FAULTLINE_TEST_SEED=witness-live-oracle", "FAULTLINE_NO_BROWSER=1", "FAULTLINE_TEST_OPENAI_URL=" + url}
	if !noKey {
		env = append(env, "OPENAI_API_KEY=fl-test-key")
	}
	return env
}

func wloExec(cmd *exec.Cmd) (string, string, int) {
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		return out.String(), errb.String() + "RUN ERROR: " + err.Error(), -2
	}
	return out.String(), errb.String(), cmd.ProcessState.ExitCode()
}

func oracleNode(t *testing.T) string {
	t.Helper()
	node := os.Getenv("FAULTLINE_ORACLE_NODE")
	if node == "" {
		node = "node"
	}
	var out, errb bytes.Buffer
	cmd := exec.Command(node, "--version")
	cmd.Stdout, cmd.Stderr = &out, &errb
	code := 0
	if err := cmd.Run(); err != nil {
		code = -1
	}
	if !oracleNodeVersionOutputMatches(runtime.GOOS, out.String(), errb.String(), code) {
		t.Fatalf("frozen oracle needs Node v22.22.2 (set FAULTLINE_ORACLE_NODE); got %q %q exit %d", out.String(), errb.String(), code)
	}
	return node
}

func newWloSide(t *testing.T, kind, node string, c wloCase) *wloSide {
	t.Helper()
	root, ws, home, tmp := wloRoots(t)
	buildWloWorkspace(t, ws, c)
	reply := c.reply
	reply.body = strings.ReplaceAll(reply.body, "@@WS@@", ws)
	p := startWloProvider(t, reply)
	repo := repoRoot(t)
	env := wloEnv(home, tmp, p.url, c.noKey)
	return &wloSide{root: root, ws: ws, provider: p, cmd: func(args []string) *exec.Cmd {
		var cmd *exec.Cmd
		if kind == "ts" {
			cmd = exec.Command(node, append([]string{"--import", filepath.Join(repo, "difftest", "gen", "seed.mjs"), "--import", filepath.Join(repo, "difftest", "gen", "fetch-shim.mjs"), filepath.Join(repo, "dist", "cli.js")}, args...)...)
		} else {
			cmd = exec.Command(flBinary, args...)
		}
		cmd.Dir, cmd.Env = ws, env
		return cmd
	}}
}

var wloPortRE = regexp.MustCompile(`127\.0\.0\.1:[0-9]+`)

// norm replaces only this side's independently generated temp root and its
// actual ephemeral loopback port.
func (s *wloSide) norm(text string) string {
	text = strings.ReplaceAll(text, s.root, "<ROOT>")
	return wloPortRE.ReplaceAllString(text, "127.0.0.1:<PORT>")
}

func (s *wloSide) invoke(extra []string) wloObservation {
	before := s.provider.count()
	stdout, stderr, code := wloExec(s.cmd(append([]string{"witness", "propose", "--live"}, extra...)))
	return wloObservation{stdout, stderr, code, s.provider.take(before)}
}

func (s *wloSide) tree(t *testing.T) map[string]string {
	t.Helper()
	return treeEntries(t, s.ws, func(rel string, b []byte) string {
		sum := sha256.Sum256([]byte(s.norm(string(b))))
		return fmt.Sprintf("file %d %s", len(b), hex.EncodeToString(sum[:]))
	})
}

func TestLiveWitnessProposalOracle(t *testing.T) {
	oracle.Skip(t)
	if runtime.GOOS == "windows" {
		t.Skip("frozen oracle comparison is POSIX only")
	}
	node := oracleNode(t)
	for _, c := range wloCases() {
		t.Run(strings.ReplaceAll(c.name, " ", "_"), func(t *testing.T) {
			ts, gof := newWloSide(t, "ts", node, c), newWloSide(t, "go", node, c)
			invocations := [][]string{c.args}
			if c.args == nil {
				invocations = [][]string{wloStdArgs}
			}
			invocations = append(invocations, c.second...)
			for i, extra := range invocations {
				tsObs, goObs := ts.invoke(extra), gof.invoke(extra)
				if strings.Contains(tsObs.stdout, `"source": "GPT-5.6"`) {
					if !strings.Contains(goObs.stdout, `"source": "GPT-6.1"`) {
						t.Fatalf("invocation %d: Go stdout lacks GPT-6.1 source:\n%s", i, goObs.stdout)
					}
					tsObs.stdout = strings.Replace(tsObs.stdout, `"source": "GPT-5.6"`, `"source": "GPT-6.1"`, 1)
				}
				if !slices.Contains(extra, "--model") && len(tsObs.requests) > 0 {
					for _, r := range tsObs.requests {
						if !strings.Contains(r, `"model":"gpt-5.6"`) {
							t.Fatalf("invocation %d: frozen request lacks gpt-5.6: %q", i, r)
						}
					}
					for _, r := range goObs.requests {
						if !strings.Contains(r, `"model":"gpt-6.1"`) {
							t.Fatalf("invocation %d: Go request lacks gpt-6.1: %q", i, r)
						}
					}
					for j := range tsObs.requests {
						tsObs.requests[j] = strings.Replace(tsObs.requests[j], `"model":"gpt-5.6"`, `"model":"gpt-6.1"`, 1)
					}
				}
				for _, o := range []struct {
					side *wloSide
					obs  *wloObservation
				}{{ts, &tsObs}, {gof, &goObs}} {
					o.obs.stdout, o.obs.stderr = o.side.norm(o.obs.stdout), o.side.norm(o.obs.stderr)
					for j := range o.obs.requests {
						o.obs.requests[j] = o.side.norm(o.obs.requests[j])
					}
				}
				if tsObs.code != goObs.code || tsObs.stdout != goObs.stdout || tsObs.stderr != goObs.stderr {
					t.Fatalf("invocation %d differs\nts: code=%d\nstdout=%q\nstderr=%q\ngo: code=%d\nstdout=%q\nstderr=%q", i, tsObs.code, tsObs.stdout, tsObs.stderr, goObs.code, goObs.stdout, goObs.stderr)
				}
				if !slices.Equal(tsObs.requests, goObs.requests) {
					t.Fatalf("invocation %d provider requests differ\nts: %q\ngo: %q", i, tsObs.requests, goObs.requests)
				}
				tsTree, goTree := ts.tree(t), gof.tree(t)
				if len(tsTree) != len(goTree) {
					t.Fatalf("invocation %d trees differ\nts: %v\ngo: %v", i, tsTree, goTree)
				}
				for k, v := range tsTree {
					if goTree[k] != v {
						t.Fatalf("invocation %d tree entry %s: ts %q go %q", i, k, v, goTree[k])
					}
				}
				t.Logf("invocation %d: code=%d requests=%d entries=%d", i, tsObs.code, len(tsObs.requests), len(tsTree))
			}
		})
	}
}

// TestLiveWitnessProposalApprovedDeviation pins the one approved difference
// without the frozen run: the Go default is gpt-6.1 with output source
// GPT-6.1, while the API-key refusal keeps the frozen GPT-5.6 text.
func TestLiveWitnessProposalApprovedDeviation(t *testing.T) {
	_, ws, home, tmp := wloRoots(t)
	buildWloWorkspace(t, ws, wloCase{})
	p := startWloProvider(t, wloRaw(wloProposal(`[]`)))
	run := func(noKey bool, extra ...string) (string, string, int) {
		cmd := exec.Command(flBinary, append([]string{"witness", "propose", "--live"}, extra...)...)
		cmd.Dir, cmd.Env = ws, wloEnv(home, tmp, p.url, noKey)
		return wloExec(cmd)
	}
	stdout, stderr, code := run(false, wloStdArgs...)
	reqs := p.take(0)
	if code != 0 || stderr != "" || len(reqs) != 1 || !strings.Contains(reqs[0], `"model":"gpt-6.1"`) || strings.Contains(reqs[0], "gpt-5.6") || !strings.Contains(stdout, `"source": "GPT-6.1"`) || strings.Contains(stdout, "GPT-5.6") {
		t.Fatalf("code=%d stderr=%q stdout=%s reqs=%q", code, stderr, stdout, reqs)
	}
	_, stderr, code = run(true, "--incident", "incident.json", "--proposal-id", "live-2", "--overlay-root", "overlays")
	if code != 1 || !strings.Contains(stderr, "OPENAI_API_KEY is required for a live GPT-5.6 witness proposal.") || p.count() != 1 {
		t.Fatalf("api-key refusal changed: code=%d stderr=%q requests=%d", code, stderr, p.count())
	}
}
