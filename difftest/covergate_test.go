//go:build faultline_test

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
// -cover over the gated packages (gatedDirs) and runs the golden and writer
// replays with GOCOVERDIR set; after a full run (no -test.run), every
// statement in a gated package that builds an error literal must have run in
// some golden or writer case, unless the literal is listed in
// unreachableLiterals, its line carries raceOnlyMarker, or its file is
// live-gated. cmd/fl is listed too: a binary whose main package is not
// instrumented writes no coverage data at all.
var coverPkgs = func() string {
	pkgs := []string{"github.com/Mizore66/faultline/cmd/fl"}
	for _, dir := range gatedDirs {
		pkgs = append(pkgs, "github.com/Mizore66/faultline/"+dir+"/...")
	}
	return strings.Join(pkgs, ",")
}()

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
		return fmt.Errorf("coverage gate: the profile has no blocks of the gated packages")
	}
	var sites []errorSite
	for _, dir := range gatedDirs {
		found, err := errorSites(filepath.Join(repo, filepath.FromSlash(dir)))
		if err != nil {
			return err
		}
		sites = append(sites, found...)
	}
	var missed bytes.Buffer
	for _, s := range sites {
		rel, _ := filepath.Rel(repo, s.file)
		rel = filepath.ToSlash(rel)
		if liveGatedFiles[rel] != "" {
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
		// only be reached by a file that changes between two walks, and one
		// marked unportedCallerMarker only by a command not ported yet.
		if line := sourceLine(s.file, s.line); !inBlock || strings.Contains(line, raceOnlyMarker) || strings.Contains(line, unportedCallerMarker) {
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
// verified (or a file changing between two checks of fl) can reach.
const raceOnlyMarker = "coverage gate: race only"

// unportedCallerMarker marks an error site in a shared helper that only a
// command not yet ported reaches (its text is reached elsewhere, so
// unreachableLiterals cannot list it); the comment names the command.
const unportedCallerMarker = "coverage gate: unported caller"

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
