//go:build faultline_test

package difftest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// These library paths have no shipped CLI producer. They remain in both
// original gates; only freshly tested native evidence supplements their data.
var nativeCoverageFiles = []string{
	"internal/demofixture/native_analysis.go",
	"internal/demofixture/native_run.go",
	"internal/demofixture/native_witness.go",
}

func nativeLiteralOutputs(repo, pos, goldens string, evidence nativeEvidence) string {
	rel, err := filepath.Rel(repo, pos)
	if err == nil {
		file, _, _ := strings.Cut(filepath.ToSlash(rel), ":")
		if slices.Contains(nativeCoverageFiles, file) {
			return evidence.outputs
		}
	}
	return goldens
}

type nativeEvidence struct {
	outputs string
	blocks  map[string][]coverBlock
}

var nativeEvidenceCache struct {
	sync.Mutex
	binding  string
	evidence nativeEvidence
}

func nativeLibraryEvidence(repo string) (nativeEvidence, error) {
	nativeEvidenceCache.Lock()
	defer nativeEvidenceCache.Unlock()
	binding, err := nativeSourceBinding(repo)
	if err != nil {
		return nativeEvidence{}, err
	}
	if binding == nativeEvidenceCache.binding {
		return nativeEvidenceCache.evidence, nil
	}
	data, err := os.ReadFile(filepath.Join(repo, "difftest/testdata/native-demo/native-contract.jsonl"))
	if err != nil {
		return nativeEvidence{}, err
	}
	var cases []string
	var expected strings.Builder
	for _, line := range bytes.Split(bytes.TrimSuffix(data, []byte{'\n'}), []byte{'\n'}) {
		var row struct {
			ID       string `json:"id"`
			Expected string `json:"expected"`
		}
		if err := json.Unmarshal(line, &row); err != nil {
			return nativeEvidence{}, err
		}
		if row.ID == "" || row.Expected == "" || slices.Contains(cases, row.ID) {
			return nativeEvidence{}, fmt.Errorf("native contract has an empty or duplicate case")
		}
		cases = append(cases, row.ID)
		expected.WriteString(row.Expected)
	}
	if len(cases) != 3 {
		return nativeEvidence{}, fmt.Errorf("native contract needs its three complete reference cases")
	}
	dir, err := os.MkdirTemp("", "faultline-native-coverage-")
	if err != nil {
		return nativeEvidence{}, err
	}
	defer os.RemoveAll(dir)
	profile := filepath.Join(dir, "profile.txt")
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-json", "-count=1", "-timeout=150s", "-coverprofile="+profile,
		"-run=^(TestNativeDiagnostics|TestNativeDiagnosticInvariants|TestNativeAnalysisFullContract)$", "./internal/demofixture",
		"-native-contract="+filepath.Join(repo, "difftest/testdata/native-demo/native-contract.jsonl"))
	cmd.Dir, cmd.WaitDelay = repo, time.Second
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if !slices.Contains([]string{"GOTOOLCHAIN", "GOPROXY", "GOSUMDB", "GOFLAGS", "GOCOVERDIR"}, key) {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr // nil stdin is closed by os/exec.
	if err := cmd.Run(); err != nil {
		return nativeEvidence{}, fmt.Errorf("native library evidence: %w\n%s\n%s", err, stdout.String(), stderr.String())
	}
	outputs, err := nativeOutputs(stdout.Bytes(), cases)
	if err != nil {
		return nativeEvidence{}, err
	}
	// The existing test compared every byte before each required case passed.
	// These COMPLETE expectations own successful prose, never rejected calls.
	outputs += expected.String()
	profileData, err := os.ReadFile(profile)
	if err != nil {
		return nativeEvidence{}, err
	}
	blocks, err := nativeProfile(profileData)
	if err != nil {
		return nativeEvidence{}, err
	}
	sites, err := errorSites(filepath.Join(repo, "internal/demofixture"))
	if err != nil {
		return nativeEvidence{}, err
	}
	var nativeSites []errorSite
	for _, site := range sites {
		rel, err := filepath.Rel(repo, site.file)
		if err != nil {
			return nativeEvidence{}, err
		}
		if slices.Contains(nativeCoverageFiles, filepath.ToSlash(rel)) {
			nativeSites = append(nativeSites, site)
		}
	}
	evidence := nativeEvidence{outputs: outputs, blocks: blocks}
	if err := validateNativeEvidence(repo, nativeSites, evidence, unreachableLiterals); err != nil {
		return nativeEvidence{}, err
	}
	after, err := nativeSourceBinding(repo)
	if err != nil {
		return nativeEvidence{}, err
	}
	if after != binding {
		return nativeEvidence{}, fmt.Errorf("native evidence inputs changed while tests ran")
	}
	nativeEvidenceCache.binding, nativeEvidenceCache.evidence = binding, evidence
	return evidence, nil
}

// Bind package sources, test helpers, transitive internal sources and the
// independent corpus. An in-process cache is never reusable after an edit.
func nativeSourceBinding(repo string) (string, error) {
	h := sha256.New()
	add := func(path string) error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(repo, path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(rel), len(data))
		h.Write(data)
		return nil
	}
	if err := filepath.WalkDir(filepath.Join(repo, "internal"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Type().IsRegular() && strings.HasSuffix(path, ".go") {
			return add(path)
		}
		return nil
	}); err != nil {
		return "", err
	}
	for _, path := range []string{"go.mod", "go.sum", "difftest/testdata/native-demo/native-contract.jsonl"} {
		if err := add(filepath.Join(repo, filepath.FromSlash(path))); err != nil {
			return "", err
		}
	}
	return repo + ":" + fmt.Sprintf("%x", h.Sum(nil)), nil
}

func nativeOutputs(data []byte, cases []string) (string, error) {
	const pkg = "github.com/Mizore66/faultline/internal/demofixture"
	parents := []string{"TestNativeDiagnostics", "TestNativeDiagnosticInvariants", "TestNativeAnalysisFullContract"}
	required := append([]string{}, parents...)
	for _, name := range []string{"timeline", "partial", "subsets", "environment"} {
		required = append(required, "TestNativeDiagnosticInvariants/"+name)
	}
	for _, name := range cases {
		required = append(required, "TestNativeAnalysisFullContract/"+name)
	}
	if len(cases) == 0 || !bytes.HasSuffix(data, []byte{'\n'}) {
		return "", fmt.Errorf("native events or contract cases are incomplete")
	}
	running, passed, diagnostic := map[string]bool{}, map[string]bool{}, map[string]string{}
	started, ended := false, false
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var event struct{ Action, Package, Test, Output string }
		err := decoder.Decode(&event)
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("native event JSON: %w", err)
		}
		if event.Package != pkg || ended {
			return "", fmt.Errorf("native events have a foreign package or follow completion")
		}
		if event.Action == "fail" || event.Action == "skip" {
			return "", fmt.Errorf("native case %s did not pass: %s", event.Test, event.Action)
		}
		if event.Test == "" {
			switch event.Action {
			case "start":
				if started {
					return "", fmt.Errorf("duplicate native package start")
				}
				started = true
			case "pass":
				if !started {
					return "", fmt.Errorf("native package passed without starting")
				}
				ended = true
			case "output":
				if strings.Contains(event.Output, "NATIVE-DIAGNOSTIC") {
					return "", fmt.Errorf("native diagnostic has no emitting case")
				}
			default:
				return "", fmt.Errorf("unexpected native package action %q", event.Action)
			}
			continue
		}
		top, _, _ := strings.Cut(event.Test, "/")
		if !started || !slices.Contains(parents, top) {
			return "", fmt.Errorf("unexpected native test %q", event.Test)
		}
		switch event.Action {
		case "run":
			if running[event.Test] || passed[event.Test] {
				return "", fmt.Errorf("duplicate native test %s", event.Test)
			}
			running[event.Test] = true
		case "pass":
			if !running[event.Test] {
				return "", fmt.Errorf("native test %s passed without running", event.Test)
			}
			delete(running, event.Test)
			passed[event.Test] = true
		case "output":
			if _, text, found := strings.Cut(event.Output, "NATIVE-DIAGNOSTIC "); found {
				if !running[event.Test] || !strings.HasPrefix(event.Test, "TestNativeDiagnostics/") || diagnostic[event.Test] != "" {
					return "", fmt.Errorf("native diagnostic has invalid or duplicate emitter %s", event.Test)
				}
				var message string
				if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &message); err != nil || message == "" {
					return "", fmt.Errorf("native diagnostic is not a nonempty JSON string")
				}
				diagnostic[event.Test] = message
			}
		case "pause", "cont":
			if !running[event.Test] {
				return "", fmt.Errorf("native case resumed without running")
			}
		default:
			return "", fmt.Errorf("unexpected native test action %q", event.Action)
		}
	}
	if !ended || len(running) != 0 || len(diagnostic) == 0 {
		return "", fmt.Errorf("native evidence has no complete executed diagnostics")
	}
	for _, name := range required {
		if !passed[name] {
			return "", fmt.Errorf("native evidence is missing passing test %s", name)
		}
	}
	for name := range passed {
		if strings.HasPrefix(name, "TestNativeDiagnostics/") && diagnostic[name] == "" {
			return "", fmt.Errorf("native case %s emitted no checked diagnostic", name)
		}
	}
	var outputs strings.Builder
	for _, name := range slices.Sorted(maps.Keys(diagnostic)) {
		if !passed[name] {
			return "", fmt.Errorf("native diagnostic case %s never passed", name)
		}
		outputs.WriteString(strconv.Quote(diagnostic[name]) + "\n")
	}
	return outputs.String(), nil
}

func nativeProfile(data []byte) (map[string][]coverBlock, error) {
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) < 2 || !slices.Contains([]string{"mode: set", "mode: count", "mode: atomic"}, lines[0]) {
		return nil, fmt.Errorf("native coverage profile has no valid mode or blocks")
	}
	blocks := map[string][]coverBlock{}
	for _, line := range lines[1:] {
		name, rest, ok := strings.Cut(line, ":")
		fields := strings.Fields(rest)
		if !ok || len(fields) != 3 {
			return nil, fmt.Errorf("malformed native coverage block %q", line)
		}
		span := strings.Split(fields[0], ",")
		if len(span) != 2 {
			return nil, fmt.Errorf("malformed native coverage span")
		}
		var positions [4]int
		for i, point := range span {
			pair := strings.Split(point, ".")
			if len(pair) != 2 {
				return nil, fmt.Errorf("malformed native coverage position")
			}
			for j, number := range pair {
				n, err := strconv.Atoi(number)
				if err != nil || n < 1 {
					return nil, fmt.Errorf("invalid native coverage position")
				}
				positions[i*2+j] = n
			}
		}
		statements, e1 := strconv.Atoi(fields[1])
		count, e2 := strconv.Atoi(fields[2])
		if e1 != nil || e2 != nil || statements < 0 || count < 0 || positions[0] > positions[2] || positions[0] == positions[2] && positions[1] > positions[3] {
			return nil, fmt.Errorf("invalid native coverage counts or span")
		}
		rel := strings.TrimPrefix(name, "github.com/Mizore66/faultline/")
		if slices.Contains(nativeCoverageFiles, rel) {
			blocks[rel] = append(blocks[rel], coverBlock{positions[0], positions[2], count})
		}
	}
	for _, name := range nativeCoverageFiles {
		if len(blocks[name]) == 0 {
			return nil, fmt.Errorf("native coverage profile has no blocks for %s", name)
		}
	}
	return blocks, nil
}

func validateNativeEvidence(repo string, sites []errorSite, evidence nativeEvidence, exemptions map[string]string) error {
	for file := range evidence.blocks {
		if !slices.Contains(nativeCoverageFiles, file) {
			return fmt.Errorf("native evidence cannot own %s", file)
		}
	}
	for _, file := range nativeCoverageFiles {
		if len(evidence.blocks[file]) == 0 {
			return fmt.Errorf("native evidence has no blocks for %s", file)
		}
	}
	for _, site := range sites {
		rel, err := filepath.Rel(repo, site.file)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !slices.Contains(nativeCoverageFiles, rel) {
			return fmt.Errorf("native evidence cannot judge %s", rel)
		}
		covered := false
		for _, block := range evidence.blocks[rel] {
			covered = covered || block.start <= site.line && site.line <= block.end && block.count > 0
		}
		reached := true
		for _, fragment := range messageFragments(site.text) {
			quoted := strconv.Quote(fragment)
			reached = reached && strings.Contains(evidence.outputs, quoted[1:len(quoted)-1])
		}
		if reason, exempt := exemptions[site.text]; exempt {
			if strings.TrimSpace(reason) == "" || covered || reached {
				return fmt.Errorf("native exemption for %q is unexplained or stale", site.text)
			}
			continue
		}
		if !covered || !reached {
			return fmt.Errorf("%s:%d: native library evidence lacks executed position or produced text for %q (covered=%v, reached=%v)", rel, site.line, site.text, covered, reached)
		}
	}
	return nil
}

func mergeNativeCoverage(blocks, native map[string][]coverBlock) error {
	for file := range native {
		if !slices.Contains(nativeCoverageFiles, file) {
			return fmt.Errorf("native evidence cannot replace replay coverage for %s", file)
		}
	}
	for file, found := range native {
		blocks[file] = append(blocks[file], found...)
	}
	return nil
}
