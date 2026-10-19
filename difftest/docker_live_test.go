//go:build faultline_test && unix

package difftest

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// realDockerImage is the image TestLiveRealDocker runs the witness in: the
// node:22-alpine image index (every platform), pinned by digest. CI pulls
// it first; investigate runs Docker with --pull never.
const realDockerImage = "node:22-alpine@sha256:0a7108bf6c7bf5de370ffb1a3ed6be93d405b43ff159f681a8d18c0e2bc2e402"

// realDockerWitness passes while state.txt is not "bad".
const realDockerWitness = `import { readFileSync } from "node:fs";
const state = readFileSync("state.txt", "utf8").trim();
console.log("state=" + state);
console.log('FAULTLINE_WITNESS_RESULT={"protocol":"faultline.witness-result.v1","outcome":"' + (state === "bad" ? "PREDICATE_FAIL" : "PREDICATE_PASS") + '"}');
process.exitCode = state === "bad" ? 1 : 0;
`

// realDockerMasked are the run fields that hold timings or nonces, and the
// identifiers derived from them (spec 4.1).
var realDockerMasked = map[string]bool{
	"startedAt": true, "finishedAt": true, "durationMs": true, "executionNonce": true, "executionId": true, "runId": true,
	"executionIds": true, "runIds": true,
}

// runFile is a run's artifact name in a bundle: runs/<runId hex>.json.
var runFile = regexp.MustCompile(`^runs/[0-9a-f]{64}\.json$`)

// TestLiveRealDocker runs `investigate git` on a two-state repository with
// the real Docker daemon (no fake Docker; the recorder stays on PATH), in
// frozen TS and then Go in the same workspace path, both unseeded, and
// compares the verdict of each state, the transitions, the run catalog with
// timings and nonces masked, the bundle's file list and the docker
// subcommands; each bundle must verify VALID under both verifiers. Bytes
// are not compared: container timing differs.
func TestLiveRealDocker(t *testing.T) {
	if os.Getenv("FAULTLINE_REAL_DOCKER") != "1" {
		t.Skip("set FAULTLINE_REAL_DOCKER=1 (needs a Docker daemon and " + realDockerImage + ")")
	}
	oracle.Skip(t)
	if out, err := exec.Command("docker", "image", "inspect", realDockerImage).CombinedOutput(); err != nil {
		t.Fatalf("docker image inspect %s (pull it first): %v\n%s", realDockerImage, err, out)
	}
	setWriterUmask(t)
	repo := repoRoot(t)
	r := makeWriterRoots(t)
	type side struct {
		name    string
		command []string
	}
	type outcome struct {
		stdout, stderr string
		exit           int
		verdicts       string
		runs           string
		files          []string
		argv           []string
	}
	var outcomes []outcome
	// Unseeded: the real clock, UUIDs and temporary names, as in production.
	for _, s := range []side{{"TS", []string{"node", filepath.Join(repo, "dist", "cli.js")}}, {"Go", []string{flBinary}}} {
		buildRealDockerWorkspace(t, repo, r)
		os.Remove(r.argvLog)
		stdout, stderr, code := runInCase(t, repo, r, "", s.command,
			"investigate", "git", "--repo", "repo", "--from", "HEAD~1", "--to", "HEAD", "--proposal", "demo-1", "--image", realDockerImage)
		if code != 0 {
			t.Fatalf("%s investigate exit %d\n%s%s", s.name, code, stdout, stderr)
		}
		dir := proofDirectory(t, stdout)
		root := jsonField(stdout, "proofBundle", "rootDigest")
		ok := func(out string, code int) bool {
			return code == 0 && strings.HasPrefix(out, "Integrity: VALID\n") && strings.Contains(out, "\nExternal root: MATCH\n")
		}
		goOut, goErr, goCode := runFlOnce(t, repo, nil, "verify", dir, "--expect-root", root)
		if !ok(goOut, goCode) {
			t.Fatalf("Go verify of the %s bundle (exit %d)\n%s%s", s.name, goCode, goOut, goErr)
		}
		if out, code := runTSVerify(t, repo, dir, root); !ok(out, code) {
			t.Fatalf("TS verify of the %s bundle (exit %d)\n%s", s.name, code, out)
		}
		v, err := jsjson.Parse(stdout)
		if err != nil {
			t.Fatal(err)
		}
		inv := v.Get("investigation")
		verdicts := jsjson.NewObj()
		for _, k := range []string{"status", "stableStates", "transitions", "nonMonotonic", "errors"} {
			verdicts.Set(k, maskRealDocker(inv.Get(k)))
		}
		var files []string
		filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				t.Fatal(err)
			}
			rel, _ := filepath.Rel(dir, p)
			rel = filepath.ToSlash(rel)
			if runFile.MatchString(rel) {
				rel = "runs/<RUN>.json"
			}
			files = append(files, rel)
			return nil
		})
		slices.Sort(files)
		var argv []string
		for _, record := range readArgvLog(r.argvLog) {
			if strings.HasPrefix(record, "docker ") {
				argv = append(argv, record[:strings.Index(record+"\x1f", "\x1f")])
			}
		}
		outcomes = append(outcomes, outcome{stdout, stderr, code, canonicalText(t, jsjson.MakeObject(verdicts)),
			canonicalText(t, maskRealDocker(inv.Get("runs"))), files, argv})
	}
	ts, goRes := outcomes[0], outcomes[1]
	if !strings.Contains(ts.verdicts, `"kind":"PASS_TO_FAIL"`) || !strings.Contains(ts.verdicts, `"verdict":"PASS"`) {
		t.Fatalf("TS did not find the PASS to FAIL transition:\n%s\n%s", ts.verdicts, ts.stdout)
	}
	if ts.verdicts != goRes.verdicts {
		t.Errorf("verdicts differ\n--- Go\n%s\n--- TS\n%s", goRes.verdicts, ts.verdicts)
	}
	if ts.runs != goRes.runs {
		t.Errorf("run catalogs differ\n--- Go\n%s\n--- TS\n%s", goRes.runs, ts.runs)
	}
	if !slices.Equal(ts.files, goRes.files) {
		t.Errorf("bundle files differ\n--- Go\n%s\n--- TS\n%s", strings.Join(goRes.files, "\n"), strings.Join(ts.files, "\n"))
	}
	if !slices.Equal(ts.argv, goRes.argv) || len(ts.argv) == 0 {
		t.Errorf("docker subcommands differ\n--- Go\n%q\n--- TS\n%q", goRes.argv, ts.argv)
	}
	if ts.stderr != goRes.stderr {
		t.Errorf("stderr differs\n--- Go\n%s\n--- TS\n%s", goRes.stderr, ts.stderr)
	}
}

// maskRealDocker replaces the realDockerMasked members of v, at any depth.
func maskRealDocker(v jsjson.Value) jsjson.Value {
	switch v.Kind() {
	case jsjson.Array:
		items := make([]jsjson.Value, len(v.Items()))
		for i, item := range v.Items() {
			items[i] = maskRealDocker(item)
		}
		return jsjson.MakeArray(items)
	case jsjson.Object:
		o := jsjson.NewObj()
		for _, k := range v.Obj().Keys() {
			if realDockerMasked[k] {
				o.Set(k, jsjson.MakeString("<MASKED>"))
			} else {
				o.Set(k, maskRealDocker(v.Obj().Field(k)))
			}
		}
		return jsjson.MakeObject(o)
	}
	return v
}

func canonicalText(t *testing.T, v jsjson.Value) string {
	t.Helper()
	text, err := canonical.CanonicalJSON(v)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

// buildRealDockerWorkspace empties the case's workspace and TMPDIR and
// builds the same state each time: repo/ with a good then a bad commit
// (fixed dates), and the proposal proposed, approved and frozen into repo/
// by the Go fl with a fixed seed.
func buildRealDockerWorkspace(t *testing.T, repo string, r writerRoots) {
	t.Helper()
	for _, dir := range []string{r.ws, r.tmp} {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", filepath.Join(r.ws, "repo")}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=FaultLine", "GIT_AUTHOR_EMAIL=faultline@example.test",
			"GIT_COMMITTER_NAME=FaultLine", "GIT_COMMITTER_EMAIL=faultline@example.test",
			"GIT_AUTHOR_DATE=2026-07-16T10:00:00Z", "GIT_COMMITTER_DATE=2026-07-16T10:00:00Z")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.Mkdir(filepath.Join(r.ws, "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	git("init", "-q", "-b", "main")
	for _, state := range []string{"good", "bad"} {
		if err := os.WriteFile(filepath.Join(r.ws, "repo", "state.txt"), []byte(state+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", "state.txt")
		git("commit", "-q", "-m", state)
	}
	proposal := `{"proposalId":"demo-1","incidentPacket":{"symptom":"The repository state must not be bad.","ciLog":"state.txt became bad",` +
		`"repositoryLanguage":"Text","repositorySummary":"Temporary repository for the real-Docker comparison."},` +
		`"witness":{"behavior":"state.txt must not be bad","command":"node witness.mjs","overlays":[{"path":"witness.mjs","bytesBase64":"` +
		base64.StdEncoding.EncodeToString([]byte(realDockerWitness)) + `"}],"policy":{"network":"disabled","credentials":"redacted","timeoutSeconds":60}}}`
	if err := os.WriteFile(filepath.Join(r.ws, "proposal.json"), []byte(proposal), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"witness", "propose", "--input", "proposal.json"},
		{"witness", "approve", "demo-1", "--approved-by", "reviewer@example.test"},
		{"witness", "freeze", "demo-1", "--repo", "repo"},
	} {
		if stdout, stderr, code := runInCase(t, repo, r, "real-docker:setup", []string{flBinary}, args...); code != 0 {
			t.Fatalf("%v: exit %d\n%s%s", args, code, stdout, stderr)
		}
	}
}

// tsCommand runs frozen TS with the seeded clock, UUIDs and mkdtemp names
// (gen-writers.ts runCli).
func tsCommand(repo string) []string {
	return []string{"node", "--import", "file://" + filepath.Join(repo, "difftest", "gen", "seed.mjs"), filepath.Join(repo, "dist", "cli.js")}
}

// runInCase runs command plus args in the case workspace with the writer
// environment and seed ("" runs unseeded), the real docker after the
// recorder on PATH.
func runInCase(t *testing.T, repo string, r writerRoots, seed string, command []string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(command[0], append(slices.Clone(command[1:]), args...)...)
	cmd.Dir = r.ws
	cmd.Env = writerEnv(repo, r, seed, writerTemplate{ID: seed})
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	var exitErr *exec.ExitError
	if err := cmd.Run(); err != nil && !errors.As(err, &exitErr) {
		t.Fatal(err)
	}
	return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}
