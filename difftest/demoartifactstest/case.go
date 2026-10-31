//go:build faultline_test

package demoartifactstest

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"github.com/Mizore66/faultline/internal/bundle/demo"
	"github.com/Mizore66/faultline/internal/demofixture"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/proofview"
)

const (
	defaultWriterOutput  = ".faultline/bundles/demo"
	defaultPreviewOutput = "out.html"
	cliTimeout           = 60 * time.Second
)

var baseDirs = map[string]string{
	"rerun":          "demo-rerun",
	"unicode":        "demo-rerun-unicode-stdout",
	"lone-surrogate": "demo-rerun-lone-surrogate-stdout",
}

func fail(format string, args ...any) { panic(AdapterError{Message: fmt.Sprintf(format, args...)}) }

func must(err error) {
	if err != nil {
		fail("%v", err)
	}
}

func outputOf(req Request) string {
	if req.Output != nil {
		return *req.Output
	}
	if req.Kind == "writer" {
		return defaultWriterOutput
	}
	return defaultPreviewOutput
}

func put(root, rel string, body []byte) {
	p := filepath.Join(root, filepath.FromSlash(rel))
	must(os.MkdirAll(filepath.Dir(p), 0o755))
	must(os.WriteFile(p, body, 0o644))
}

func link(root, rel, target string) {
	p := filepath.Join(root, filepath.FromSlash(rel))
	must(os.MkdirAll(filepath.Dir(p), 0o755))
	if err := os.Symlink(filepath.FromSlash(target), p); err != nil {
		panic(AdapterError{Message: fmt.Sprintf("symlink %s: %v", rel, err), Cause: ErrSymlinkUnavailable})
	}
}

func relTo(fromRel, toRel string) string {
	r, err := filepath.Rel(filepath.Dir(filepath.FromSlash(fromRel)), filepath.FromSlash(toRel))
	must(err)
	return filepath.ToSlash(r)
}

// copyBase copies a retained base tree from repo byte for byte.
func copyBase(root, repo, base, rel string) {
	src := filepath.Join(repo, "difftest", "testdata", "bases", base)
	must(filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		r, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		put(root, rel+"/"+filepath.ToSlash(r), b)
		return nil
	}))
}

// Prepare performs a request's setup in the fresh, empty case root. It never
// calls the writer or the CLI, never consumes the seeded UUID stream and uses
// only copied retained input bytes. It runs outside any tracing.
func Prepare(root, repo string, req Request) error {
	return prepare(root, repo, req)
}

func prepare(root, repo string, req Request) (err error) {
	defer func() {
		if r := recover(); r != nil {
			a, ok := r.(AdapterError)
			if !ok {
				panic(r)
			}
			err = a
		}
	}()
	switch req.Kind {
	case "writer", "cli", "analysis", "render":
	default:
		fail("request kind %q is not supported", req.Kind)
	}
	out := outputOf(req)
	switch req.Setup {
	case "":
	case "parent-file":
		put(root, filepath.ToSlash(filepath.Dir(out)), []byte("blocker\n"))
	case "output-file":
		put(root, out, []byte("old output\n"))
	case "output-directory", "invalid-bundle":
		put(root, out+"/sentinel.txt", []byte("keep\n"))
	case "existing-preview":
		put(root, out, []byte("old preview\n"))
	case "existing-bundle":
		copyBase(root, repo, "demo-replay", out)
	case "linked-root":
		must(os.MkdirAll(filepath.Join(root, "actual-managed"), 0o755))
		link(root, ".faultline/bundles", "../actual-managed")
	case "linked-parent":
		must(os.MkdirAll(filepath.Join(root, "actual-parent"), 0o755))
		link(root, "linked", "actual-parent")
	case "linked-output":
		if req.Kind == "writer" {
			copyBase(root, repo, "demo-replay", "target-bundle")
			link(root, out, relTo(out, "target-bundle"))
		} else {
			put(root, "target.html", []byte("keep\n"))
			link(root, out, relTo(out, "target.html"))
		}
	case "dangling-output":
		link(root, out, relTo(out, "target.html"))
	case "linked-bundle-file":
		copyBase(root, repo, "demo-replay", out)
		file := out + "/witness/witness.json"
		orig, rerr := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		must(rerr)
		put(root, "original-witness.json", orig)
		must(os.Remove(filepath.Join(root, filepath.FromSlash(file))))
		link(root, file, relTo(file, "original-witness.json"))
	default:
		fail("setup %q is not supported", req.Setup)
	}
	return nil
}

func baseAnalysis(repo string, req Request) jsjson.Value {
	if req.Base == "" || req.Base == "replay" {
		return demofixture.CreateReplayAnalysis()
	}
	dir, ok := baseDirs[req.Base]
	if !ok {
		fail("unknown base %q", req.Base)
	}
	text, err := os.ReadFile(filepath.Join(repo, "difftest", "testdata", "bases", dir, "analysis.json"))
	must(err)
	v, perr := jsjson.Parse(string(text))
	must(perr)
	return v
}

func cloneObject(v jsjson.Value) *jsjson.Obj {
	o := jsjson.NewObj()
	for _, k := range v.Obj().Keys() {
		o.Set(k, v.Obj().Field(k))
	}
	return o
}

func mutate(a jsjson.Value, mutation string) jsjson.Value {
	catalog := a.Get("runCatalog").Items()
	switch mutation {
	case "":
	case "extra-keys":
		a.Obj().Set("z-extra", jsjson.MakeString("z-extra"))
		a.Obj().Set("a-extra", jsjson.MakeString("a-extra"))
	case "missing-catalog":
		a.Obj().Set("runCatalog", jsjson.MakeArray(append([]jsjson.Value(nil), catalog[1:]...)))
	case "duplicate-id":
		a.Obj().Set("runCatalog", jsjson.MakeArray(append(append([]jsjson.Value(nil), catalog...), catalog[0])))
	case "sanitized-collision":
		items := append([]jsjson.Value(nil), catalog...)
		for _, id := range []string{"collision:", "collision?"} {
			o := cloneObject(catalog[0])
			o.Set("id", jsjson.MakeString(id))
			items = append(items, jsjson.MakeObject(o))
		}
		a.Obj().Set("runCatalog", jsjson.MakeArray(items))
	case "no-boundary":
		a.Obj().Set("transitions", jsjson.MakeArray([]jsjson.Value{}))
	case "escape-fallback":
		const hostile = "<>&\"'"
		a.Get("witness").Obj().Set("behavior", jsjson.MakeString(hostile))
		a.Get("claims").Items()[0].Obj().Set("statement", jsjson.MakeString(hostile))
		a.Get("prevention", "lastGood").Obj().Set("stateId", jsjson.MakeString("<unknown-state>"))
	case "non-replay":
		a.Obj().Set("mode", jsjson.MakeString("RERUN"))
	default:
		fail("mutation %q is not supported", mutation)
	}
	return a
}

// Inventory is the byte-path-sorted lstat inventory of root; links are
// recorded, never followed.
func Inventory(root string) ([]Entry, error) {
	out := []Entry{}
	var walk func(dir, prefix string) error
	walk = func(dir, prefix string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			p := filepath.Join(dir, e.Name())
			rel := e.Name()
			if prefix != "" {
				rel = prefix + "/" + e.Name()
			}
			info, err := os.Lstat(p)
			if err != nil {
				return err
			}
			switch {
			case info.Mode()&fs.ModeSymlink != 0:
				target, err := os.Readlink(p)
				if err != nil {
					return err
				}
				target = filepath.ToSlash(target)
				out = append(out, Entry{Path: rel, Kind: "symlink", Target: &target})
			case info.IsDir():
				out = append(out, Entry{Path: rel, Kind: "directory"})
				if err := walk(p, rel); err != nil {
					return err
				}
			case info.Mode().IsRegular():
				b, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				enc := base64.StdEncoding.EncodeToString(b)
				out = append(out, Entry{Path: rel, Kind: "file", Base64: &enc})
			default:
				return fmt.Errorf("unsupported fixture entry %s", rel)
			}
		}
		return nil
	}
	if err := walk(root, ""); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// libraryCall runs the request's real library function. A thrown error is the
// call's data; only unsupported requests are adapter failures.
func libraryCall(repo string, req Request) *Call {
	call := &Call{}
	value := func(v jsjson.Value) { s := jsjson.Stringify(v); call.ValueJSON = &s }
	failure := func(err error) { m := jsexc.Message(err); call.Error = &m }
	switch req.Kind {
	case "analysis":
		value(demofixture.CreateReplayAnalysis())
	case "render":
		var page string
		var err error
		if req.ID == "render-default" {
			page, err = proofview.RenderJudgePreviewPage(nil)
		} else {
			analysis := mutate(baseAnalysis(repo, req), req.Mutation)
			page, err = proofview.RenderJudgePreviewPage(&analysis)
		}
		if err != nil {
			failure(err)
		} else {
			value(jsjson.MakeString(page))
		}
	case "writer":
		analysis := mutate(baseAnalysis(repo, req), req.Mutation)
		written, err := demo.Write(outputOf(req), analysis, demo.WriteOptions{ProofRoot: req.ProofRoot})
		if err != nil {
			failure(err)
		} else {
			// Ordered lowercase keys, not the exported struct's field names.
			value(jsjson.MustObject(
				"directory", jsjson.MakeString(written.Directory),
				"manifestDigest", jsjson.MakeString(written.ManifestDigest),
				"rootDigest", jsjson.MakeString(written.RootDigest)))
		}
	}
	return call
}

// cliProcess runs the actual binary once, in root, with the inherited
// environment (so GOCOVERDIR reaches an instrumented binary). Start failures,
// timeouts and signal deaths are adapter failures; a nonzero exit is data.
func cliProcess(root, cliBinary string, req Request) *Process {
	if cliBinary == "" {
		fail("CLI request %q needs the actual fl binary", req.ID)
	}
	args := []string{"judge-preview"}
	if req.Argv != nil {
		for _, a := range *req.Argv {
			// The capture's one absolute-path case names its output by absolute path.
			if req.ID == "cli-absolute" && req.Output != nil && a == *req.Output {
				a = filepath.Join(root, filepath.FromSlash(a))
			}
			args = append(args, a)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), cliTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, cliBinary, args...)
	cmd.Dir, cmd.Env = root, os.Environ()
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		fail("CLI %q timed out", req.ID)
	}
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		fail("CLI %q did not run: %v", req.ID, err)
	}
	code := cmd.ProcessState.ExitCode()
	if code < 0 {
		fail("CLI %q has no exit status: %v", req.ID, cmd.ProcessState)
	}
	return &Process{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: code}
}

// Invoke executes the requested real call with the process cwd already at
// root (the driver changes it once; an in-process caller must too) and returns
// the unmasked Capture. It does no setup or cleanup; the inventories bracket
// the call. A library request returns Call (null Process); a CLI request runs
// the actual cliBinary and returns Process (null Call).
func Invoke(root, repo, cliBinary string, req Request) Capture {
	switch req.Kind {
	case "writer", "analysis", "render", "cli":
	default:
		fail("request kind %q is not supported", req.Kind)
	}
	before, err := Inventory(root)
	must(err)
	capture := Capture{Schema: CaptureSchema, Request: req, Before: before}
	if req.Kind == "cli" {
		capture.Process = cliProcess(root, cliBinary, req)
	} else {
		capture.Call = libraryCall(repo, req)
	}
	after, err := Inventory(root)
	must(err)
	capture.After = after
	return capture
}
