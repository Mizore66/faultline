//go:build faultline_test

package gitinv

// SetGitTimeoutForTest swaps GIT_MATERIALIZATION_TIMEOUT_MS (in ms); the
// returned func restores it.
func SetGitTimeoutForTest(ms int) (restore func()) {
	old := gitMaterializationTimeoutMs
	gitMaterializationTimeoutMs = ms
	return func() { gitMaterializationTimeoutMs = old }
}
