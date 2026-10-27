//go:build faultline_test

package provenance

import "github.com/Mizore66/faultline/internal/proofview"

// SetViewLoaderForTest replaces loadVerifiedGitProofView for every call in
// this package (the live tests memoize it per bundle, as the oracle's
// difftest-memo module does for TS); the returned func restores it.
func SetViewLoaderForTest(load func(bundleDirectory string) (proofview.View, error)) (restore func()) {
	saved := loadVerifiedView
	loadVerifiedView = load
	return func() { loadVerifiedView = saved }
}
