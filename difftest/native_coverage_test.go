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
	"internal/cli/judge_demo.go",
	"internal/bundle/demo/native_evidence.go",
	"internal/proofview/incident.go",
	"internal/proofview/incident_server.go",
}

// nativeEvidencePackage is one exact package allowed to produce native evidence.
// Only a diagnostic parent may emit NATIVE-DIAGNOSTIC, and only demofixture
// carries the invariant children and the three contract cases.
type nativeEvidencePackage struct {
	importPath        string
	sourceDirectory   string
	parents           []string
	diagnosticParents []string
}

var (
	nativeDemofixturePackage = nativeEvidencePackage{
		importPath:        "github.com/Mizore66/faultline/internal/demofixture",
		sourceDirectory:   "internal/demofixture",
		parents:           []string{"TestNativeDiagnostics", "TestNativeDiagnosticInvariants", "TestNativeAnalysisFullContract"},
		diagnosticParents: []string{"TestNativeDiagnostics"},
	}
	nativeCLIPackage = nativeEvidencePackage{
		importPath:        "github.com/Mizore66/faultline/internal/cli",
		sourceDirectory:   "internal/cli",
		parents:           []string{"TestJudgeDemoNativeDiagnostics"},
		diagnosticParents: []string{"TestJudgeDemoNativeDiagnostics"},
	}
	nativeDemoPackage = nativeEvidencePackage{
		importPath:        "github.com/Mizore66/faultline/internal/bundle/demo",
		sourceDirectory:   "internal/bundle/demo",
		parents:           []string{"TestNativeEvidenceDiagnostics"},
		diagnosticParents: []string{"TestNativeEvidenceDiagnostics"},
	}
	// The proofview owner's diagnostic parent is the overlay drift test; its
	// start/end children each emit one checked record. The other parents must
	// pass but never emit.
	nativeProofviewPackage = nativeEvidencePackage{
		importPath:      "github.com/Mizore66/faultline/internal/proofview",
		sourceDirectory: "internal/proofview",
		parents: []string{
			"TestInteractiveIncidentPage", "TestInteractiveIncidentReplaceOnceDrift", "TestInteractiveIncidentListenerDrift",
			"TestIncidentServerHTTP", "TestIncidentServerSingleFlight", "TestIncidentServerClose",
		},
		diagnosticParents: []string{"TestInteractiveIncidentListenerDrift"},
	}
	nativeAllPackages = []nativeEvidencePackage{nativeDemofixturePackage, nativeCLIPackage, nativeDemoPackage, nativeProofviewPackage}

	nativeDemofixtureFiles = []string{
		"internal/demofixture/native_analysis.go",
		"internal/demofixture/native_run.go",
		"internal/demofixture/native_witness.go",
	}
	nativeExportFiles    = []string{"internal/cli/judge_demo.go", "internal/bundle/demo/native_evidence.go"}
	nativeProofviewFiles = []string{"internal/proofview/incident.go", "internal/proofview/incident_server.go"}

	// Committed, independently reviewed proofview expectations. They are read
	// as data, never produced by the code under test.
	nativeProofviewDiagnostics = "difftest/testdata/incident-renderer/interactive-diagnostics.jsonl"
	nativeProofviewPages       = []string{
		"difftest/testdata/incident-renderer/incident.html",
		"difftest/testdata/incident-renderer/interactive-enabled.html",
		"difftest/testdata/incident-renderer/interactive-disabled.html",
	}
)

// nativeOwner names the one package whose fresh diagnostics may satisfy an owned
// file: demofixture text only the original three files, CLI text only
// judge_demo.go, demo text only native_evidence.go.
func nativeOwner(file string) string {
	for _, pkg := range nativeAllPackages {
		if slices.Contains(nativeCoverageFiles, file) && strings.HasPrefix(file, pkg.sourceDirectory+"/") && !strings.Contains(strings.TrimPrefix(file, pkg.sourceDirectory+"/"), "/") {
			return pkg.importPath
		}
	}
	return ""
}

func nativeLiteralOutputs(repo, pos, goldens string, evidence nativeEvidence) string {
	rel, err := filepath.Rel(repo, pos)
	if err == nil {
		file, _, _ := strings.Cut(filepath.ToSlash(rel), ":")
		if slices.Contains(nativeCoverageFiles, file) {
			return evidence.outputs[nativeOwner(file)]
		}
	}
	return goldens
}

type nativeEvidence struct {
	outputs map[string]string // produced text by owning package import path
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
	proofviewExpected, err := nativeProofviewExpectedText(repo)
	if err != nil {
		return nativeEvidence{}, err
	}
	dir, err := os.MkdirTemp("", "faultline-native-coverage-")
	if err != nil {
		return nativeEvidence{}, err
	}
	defer os.RemoveAll(dir)
	// One deadline covers all three sequential subprocesses.
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	commands := nativeEvidenceCommands(repo, filepath.Join(dir, "native-profile.txt"), filepath.Join(dir, "export-profile.txt"), filepath.Join(dir, "proofview-profile.txt"))
	var streams [][]byte
	for _, command := range commands {
		cmd := exec.CommandContext(ctx, "go", command.args...)
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
		streams = append(streams, stdout.Bytes())
	}
	nativeProfilePath, exportProfilePath := commands[0].profile, commands[1].profile
	outputs, err := nativeOutputsFor(streams[0], cases, commands[0].packages)
	if err != nil {
		return nativeEvidence{}, err
	}
	exportOutputs, err := nativeOutputsFor(streams[1], nil, commands[1].packages)
	if err != nil {
		return nativeEvidence{}, err
	}
	proofviewOutputs, err := nativeOutputsFor(streams[2], nil, commands[2].packages)
	if err != nil {
		return nativeEvidence{}, err
	}
	for _, more := range []map[string]string{exportOutputs, proofviewOutputs} {
		for pkg, text := range more {
			if _, dup := outputs[pkg]; dup {
				return nativeEvidence{}, fmt.Errorf("native streams both own %s", pkg)
			}
			outputs[pkg] = text
		}
	}
	outputs[nativeProofviewPackage.importPath] += proofviewExpected
	// The existing test compared every byte before each required case passed.
	// These COMPLETE expectations own successful prose, never rejected calls.
	outputs[nativeDemofixturePackage.importPath] += expected.String()
	profileData, err := os.ReadFile(nativeProfilePath)
	if err != nil {
		return nativeEvidence{}, err
	}
	blocks, err := nativeProfileFor(profileData, nativeDemofixtureFiles)
	if err != nil {
		return nativeEvidence{}, err
	}
	exportProfileData, err := os.ReadFile(exportProfilePath)
	if err != nil {
		return nativeEvidence{}, err
	}
	exportBlocks, err := nativeProfileFor(exportProfileData, nativeExportFiles)
	if err != nil {
		return nativeEvidence{}, err
	}
	proofviewProfileData, err := os.ReadFile(commands[2].profile)
	if err != nil {
		return nativeEvidence{}, err
	}
	proofviewBlocks, err := nativeProfileFor(proofviewProfileData, nativeProofviewFiles)
	if err != nil {
		return nativeEvidence{}, err
	}
	// The subsets are disjoint: demofixture counters incidentally emitted by the
	// CLI's imports were discarded, never allowed to replace the original evidence.
	if blocks, err = mergeDisjointNativeProfiles(blocks, exportBlocks); err != nil {
		return nativeEvidence{}, err
	}
	if blocks, err = mergeDisjointNativeProfiles(blocks, proofviewBlocks); err != nil {
		return nativeEvidence{}, err
	}
	var nativeSites []errorSite
	for _, pkg := range nativeAllPackages {
		sites, err := errorSites(filepath.Join(repo, filepath.FromSlash(pkg.sourceDirectory)))
		if err != nil {
			return nativeEvidence{}, err
		}
		for _, site := range sites {
			rel, err := filepath.Rel(repo, site.file)
			if err != nil {
				return nativeEvidence{}, err
			}
			if slices.Contains(nativeCoverageFiles, filepath.ToSlash(rel)) {
				nativeSites = append(nativeSites, site)
			}
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
	inputs := append([]string{"go.mod", "go.sum", "difftest/testdata/native-demo/native-contract.jsonl", nativeProofviewDiagnostics}, nativeProofviewPages...)
	for _, path := range inputs {
		if err := add(filepath.Join(repo, filepath.FromSlash(path))); err != nil {
			return "", err
		}
	}
	return repo + ":" + fmt.Sprintf("%x", h.Sum(nil)), nil
}

// nativeProofviewExpectedText is the proofview owner's committed expected text:
// each diagnostic row decoded and quoted, then each committed page quoted, in
// the searchable form the literal gate compares against.
func nativeProofviewExpectedText(repo string) (string, error) {
	data, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(nativeProofviewDiagnostics)))
	if err != nil {
		return "", err
	}
	var out strings.Builder
	rows := 0
	for _, line := range bytes.Split(bytes.TrimSuffix(data, []byte{'\n'}), []byte{'\n'}) {
		var row struct{ ID, Text string }
		if err := json.Unmarshal(line, &row); err != nil {
			return "", err
		}
		if row.ID == "" || row.Text == "" {
			return "", fmt.Errorf("proofview diagnostics have an empty row")
		}
		out.WriteString(strconv.Quote(row.Text) + "\n")
		rows++
	}
	if rows != 5 {
		return "", fmt.Errorf("proofview diagnostics need their five committed rows")
	}
	for _, name := range nativeProofviewPages {
		page, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(name)))
		if err != nil {
			return "", err
		}
		q := strconv.Quote(string(page))
		out.WriteString(q[1 : len(q)-1])
		out.WriteByte('\n')
	}
	return out.String(), nil
}

// nativeOutputs decodes one stream that must contain every owned package.
func nativeOutputs(data []byte, cases []string) (map[string]string, error) {
	return nativeOutputsFor(data, cases, nativeAllPackages)
}

// nativeOutputsFor decodes a complete go test -json stream. Each allowed package
// must start and end exactly once with its parents, children and diagnostics
// accounted separately; a foreign package, event after completion, skip, failure,
// pass-without-run or diagnostic from the wrong test is rejected.
func nativeOutputsFor(data []byte, cases []string, allowed []nativeEvidencePackage) (map[string]string, error) {
	type state struct {
		started, ended bool
		running        map[string]bool
		passed         map[string]bool
		diagnostic     map[string][]string
	}
	packages := map[string]*state{}
	spec := map[string]nativeEvidencePackage{}
	for _, pkg := range allowed {
		packages[pkg.importPath] = &state{running: map[string]bool{}, passed: map[string]bool{}, diagnostic: map[string][]string{}}
		spec[pkg.importPath] = pkg
	}
	if len(packages) == 0 || !bytes.HasSuffix(data, []byte{'\n'}) {
		return nil, fmt.Errorf("native events or contract cases are incomplete")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var event struct{ Action, Package, Test, Output string }
		err := decoder.Decode(&event)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("native event JSON: %w", err)
		}
		st, pkg := packages[event.Package], spec[event.Package]
		if st == nil || st.ended {
			return nil, fmt.Errorf("native events have a foreign package or follow completion")
		}
		if event.Action == "fail" || event.Action == "skip" {
			return nil, fmt.Errorf("native case %s did not pass: %s", event.Test, event.Action)
		}
		if event.Test == "" {
			switch event.Action {
			case "start":
				if st.started {
					return nil, fmt.Errorf("duplicate native package start")
				}
				st.started = true
			case "pass":
				if !st.started {
					return nil, fmt.Errorf("native package passed without starting")
				}
				st.ended = true
			case "output":
				if strings.Contains(event.Output, "NATIVE-DIAGNOSTIC") {
					return nil, fmt.Errorf("native diagnostic has no emitting case")
				}
			default:
				return nil, fmt.Errorf("unexpected native package action %q", event.Action)
			}
			continue
		}
		top, _, _ := strings.Cut(event.Test, "/")
		if !st.started || !slices.Contains(pkg.parents, top) {
			return nil, fmt.Errorf("unexpected native test %q", event.Test)
		}
		switch event.Action {
		case "run":
			if st.running[event.Test] || st.passed[event.Test] {
				return nil, fmt.Errorf("duplicate native test %s", event.Test)
			}
			st.running[event.Test] = true
		case "pass":
			if !st.running[event.Test] {
				return nil, fmt.Errorf("native test %s passed without running", event.Test)
			}
			delete(st.running, event.Test)
			st.passed[event.Test] = true
		case "output":
			if _, text, found := strings.Cut(event.Output, "NATIVE-DIAGNOSTIC "); found {
				// Every package emits exactly one record from each checked child; a
				// parent, or an unrelated child of another parent, never emits.
				_, child, isChild := strings.Cut(event.Test, "/")
				if !st.running[event.Test] || !slices.Contains(pkg.diagnosticParents, top) || !isChild || child == "" || len(st.diagnostic[event.Test]) != 0 {
					return nil, fmt.Errorf("native diagnostic has invalid or duplicate emitter %s", event.Test)
				}
				var message string
				if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &message); err != nil || message == "" {
					return nil, fmt.Errorf("native diagnostic is not a nonempty JSON string")
				}
				st.diagnostic[event.Test] = append(st.diagnostic[event.Test], message)
			}
		case "pause", "cont":
			if !st.running[event.Test] {
				return nil, fmt.Errorf("native case resumed without running")
			}
		default:
			return nil, fmt.Errorf("unexpected native test action %q", event.Action)
		}
	}
	outputs := map[string]string{}
	for _, pkg := range allowed {
		st := packages[pkg.importPath]
		if !st.ended || len(st.running) != 0 || len(st.diagnostic) == 0 {
			return nil, fmt.Errorf("native evidence has no complete executed diagnostics for %s", pkg.importPath)
		}
		required := append([]string{}, pkg.parents...)
		if pkg.importPath == nativeDemofixturePackage.importPath {
			if len(cases) == 0 {
				return nil, fmt.Errorf("native events or contract cases are incomplete")
			}
			for _, name := range []string{"timeline", "partial", "subsets", "environment"} {
				required = append(required, "TestNativeDiagnosticInvariants/"+name)
			}
			for _, name := range cases {
				required = append(required, "TestNativeAnalysisFullContract/"+name)
			}
		}
		for _, name := range required {
			if !st.passed[name] {
				return nil, fmt.Errorf("native evidence is missing passing test %s in %s", name, pkg.importPath)
			}
		}
		for _, parent := range pkg.diagnosticParents {
			emitted := false
			for name := range st.passed {
				emitted = emitted || strings.HasPrefix(name, parent+"/") && len(st.diagnostic[name]) != 0
			}
			if !emitted {
				return nil, fmt.Errorf("native diagnostic parent %s emitted no checked diagnostic", parent)
			}
		}
		for name := range st.passed {
			top, _, isChild := strings.Cut(name, "/")
			if isChild && slices.Contains(pkg.diagnosticParents, top) && len(st.diagnostic[name]) == 0 {
				return nil, fmt.Errorf("native case %s emitted no checked diagnostic", name)
			}
		}
		for _, name := range slices.Sorted(maps.Keys(st.diagnostic)) {
			if !st.passed[name] {
				return nil, fmt.Errorf("native diagnostic case %s never passed", name)
			}
			for _, message := range st.diagnostic[name] {
				outputs[pkg.importPath] += strconv.Quote(message) + "\n"
			}
		}
	}
	return outputs, nil
}

// nativeEvidenceCommand is one go test invocation: its argv, coverage profile and
// the packages whose stream it must produce.
type nativeEvidenceCommand struct {
	args     []string
	profile  string
	packages []nativeEvidencePackage
}

// nativeEvidenceCommands builds, without running anything, the three sequential
// invocations. Only the demofixture binary receives -native-contract.
func nativeEvidenceCommands(repo, nativeProfile, exportProfile, proofviewProfile string) []nativeEvidenceCommand {
	base := func(profile, run string) []string {
		return []string{"test", "-json", "-count=1", "-timeout=150s", "-coverprofile=" + profile, "-run=" + run}
	}
	return []nativeEvidenceCommand{
		{
			args: append(append(base(nativeProfile, "^(TestNativeDiagnostics|TestNativeDiagnosticInvariants|TestNativeAnalysisFullContract)$"),
				"./internal/demofixture"), "-native-contract="+filepath.Join(repo, "difftest/testdata/native-demo/native-contract.jsonl")),
			profile:  nativeProfile,
			packages: []nativeEvidencePackage{nativeDemofixturePackage},
		},
		{
			args: append(base(exportProfile, "^(TestJudgeDemoNativeDiagnostics|TestNativeEvidenceDiagnostics)$"),
				"./internal/cli", "./internal/bundle/demo"),
			profile:  exportProfile,
			packages: []nativeEvidencePackage{nativeCLIPackage, nativeDemoPackage},
		},
		{
			args: append(base(proofviewProfile, "^(TestInteractiveIncidentPage|TestInteractiveIncidentReplaceOnceDrift|TestInteractiveIncidentListenerDrift|TestIncidentServerHTTP|TestIncidentServerSingleFlight|TestIncidentServerClose)$"),
				"./internal/proofview"),
			profile:  proofviewProfile,
			packages: []nativeEvidencePackage{nativeProofviewPackage},
		},
	}
}

// mergeDisjointNativeProfiles returns a new map; on overlap it fails and neither
// input is changed.
func mergeDisjointNativeProfiles(a, b map[string][]coverBlock) (map[string][]coverBlock, error) {
	out := maps.Clone(a)
	for file, found := range b {
		if _, dup := out[file]; dup {
			return nil, fmt.Errorf("native profiles both own %s", file)
		}
		out[file] = found
	}
	return out, nil
}

// nativeProfile is the all-owned-files wrapper.
func nativeProfile(data []byte) (map[string][]coverBlock, error) {
	return nativeProfileFor(data, nativeCoverageFiles)
}

// nativeProfileFor requires every expected file and retains only that subset.
func nativeProfileFor(data []byte, expectedFiles []string) (map[string][]coverBlock, error) {
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
		if slices.Contains(nativeCoverageFiles, rel) && slices.Contains(expectedFiles, rel) {
			blocks[rel] = append(blocks[rel], coverBlock{positions[0], positions[2], count})
		}
	}
	for _, name := range expectedFiles {
		if !slices.Contains(nativeCoverageFiles, name) || len(blocks[name]) == 0 {
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
			reached = reached && strings.Contains(evidence.outputs[nativeOwner(rel)], quoted[1:len(quoted)-1])
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
