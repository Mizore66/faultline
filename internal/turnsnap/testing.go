//go:build faultline_test

package turnsnap

import (
	"strings"

	"github.com/Mizore66/faultline/internal/jsjson"
)

// PlanForTest runs the planner (planTurnSnapshotPaths) with a stub Git
// runner that lists `listed` as the tracked files and no untracked ones,
// as difftest's planTurnSnapshotPaths oracle op does.
func PlanForTest(root string, listed []string, trackedFilesOnly, skipSecretScan bool) (paths, warnings []string, err error) {
	runGit := func(_ string, args []string, _ runOpts) (string, error) {
		for _, a := range args {
			if a == "--others" {
				return "", nil
			}
		}
		return strings.Join(listed, "\x00"), nil
	}
	p, err := planPaths(root, runGit, planOptions{trackedFilesOnly: trackedFilesOnly, skipSecretScan: skipSecretScan})
	if err != nil {
		return nil, nil, err
	}
	return p.paths, p.warnings, nil
}

// CaptureForTest is Capture with the default Git runner wrapped so that
// record sees each call (root, args, env additions, stdin) before it runs,
// as difftest's captureTurnTreeSnapshot oracle op wraps
// defaultTurnSnapshotGitRunner.
func CaptureForTest(repository string, o CaptureOptions, record func(root string, args, env []string, input []byte, hasInput bool)) (jsjson.Value, []string, error) {
	o.runGit = func(root string, args []string, ro runOpts) (string, error) {
		record(root, append([]string{}, args...), append([]string{}, ro.env...), ro.input, ro.hasInput)
		return defaultRunner(root, args, ro)
	}
	return Capture(repository, o)
}
