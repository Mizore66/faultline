package difftest

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// The coverage gate keys error checks by position, where
// TestGoldensReachEveryErrorLiteral keys them by text: a literal counts as
// reached there when another check prints the same or a longer text
// (round 5, §8 F10). With FAULTLINE_COVERAGE_GATE=1, TestMain builds fl with
// -cover over internal/bundle and runs the replay with GOCOVERDIR set; after
// a full run (no -test.run), every statement in internal/bundle that builds
// an error literal must have run in some golden case, unless the literal is
// listed in unreachableLiterals or its file is live-gated.
// cmd/fl is listed too: a binary whose main package is not instrumented
// writes no coverage data at all.
const coverPkgs = "github.com/Mizore66/faultline/cmd/fl,github.com/Mizore66/faultline/internal/bundle/..."

func coverageGate(repo, coverDir string) error {
	profile := filepath.Join(coverDir, "profile.txt")
	cmd := exec.Command("go", "tool", "covdata", "textfmt", "-i="+coverDir, "-o="+profile)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go tool covdata: %v: %s", err, out)
	}
	type block struct{ start, end, count int }
	blocks := map[string][]block{} // by file path relative to the module
	f, err := os.Open(profile)
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	const module = "github.com/Mizore66/faultline/"
	for scanner.Scan() {
		line := scanner.Text()
		// github.com/…/file.go:12.3,14.5 2 1
		name, rest, ok := strings.Cut(line, ":")
		if !ok || !strings.HasPrefix(name, module) {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) != 3 {
			continue
		}
		span := strings.Split(fields[0], ",")
		start, _ := strconv.Atoi(strings.Split(span[0], ".")[0])
		end, _ := strconv.Atoi(strings.Split(span[1], ".")[0])
		count, _ := strconv.Atoi(fields[2])
		rel := strings.TrimPrefix(name, module)
		blocks[rel] = append(blocks[rel], block{start, end, count})
	}
	if len(blocks) == 0 {
		return fmt.Errorf("coverage gate: the profile has no internal/bundle blocks")
	}
	sites, err := errorSites(filepath.Join(repo, "internal", "bundle"))
	if err != nil {
		return err
	}
	var missed bytes.Buffer
	for _, s := range sites {
		rel, _ := filepath.Rel(repo, s.file)
		rel = filepath.ToSlash(rel)
		if liveGatedFiles[strings.TrimPrefix(rel, "internal/bundle/")] != "" {
			continue
		}
		if _, ok := unreachableLiterals[s.text]; ok {
			continue
		}
		inBlock, covered := false, false
		for _, b := range blocks[rel] {
			if b.start <= s.line && s.line <= b.end {
				inBlock = true
				covered = covered || b.count > 0
			}
		}
		// A site in no block is a declaration (a schema's message), which
		// coverage does not instrument; TestGoldensReachEveryErrorLiteral
		// checks those by text. A site marked raceOnlyMarker on its line can
		// only be reached by a file that changes between two walks.
		if !inBlock || strings.Contains(sourceLine(s.file, s.line), raceOnlyMarker) {
			continue
		}
		if !covered {
			fmt.Fprintf(&missed, "%s:%d: no golden case runs the check that prints %q\n", rel, s.line, s.text)
		}
	}
	if missed.Len() > 0 {
		return fmt.Errorf("coverage gate: add a mutation template that reaches each of these, or list it in unreachableLiterals with the reason\n%s", missed.String())
	}
	return nil
}

// raceOnlyMarker marks an error site that only a bundle changing while it is
// verified can reach.
const raceOnlyMarker = "coverage gate: race only"

func sourceLine(file string, line int) string {
	b, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(b), "\n")
	if line < 1 || line > len(lines) {
		return ""
	}
	return lines[line-1]
}
