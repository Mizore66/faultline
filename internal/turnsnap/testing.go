//go:build faultline_test

package turnsnap

import "strings"

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
