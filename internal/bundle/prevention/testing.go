//go:build faultline_test

package prevention

import (
	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// MemoizeGitVerifyForTest makes BuildFromArtifacts verify each (bundle,
// expected root) pair once, for live tests that build from unchanging
// bundles many times (the Git verifier itself is compared elsewhere).
func MemoizeGitVerifyForTest() (restore func()) {
	type key struct{ dir, root string }
	cache := map[key]bundle.Result{}
	saved := verifyGitBundle
	verifyGitBundle = func(dir, root string, provided bool) bundle.Result {
		k := key{dir, root}
		if r, ok := cache[k]; ok {
			return r
		}
		r := saved(dir, root, provided)
		cache[k] = r
		return r
	}
	return func() { verifyGitBundle = saved }
}

// VerifiedRootDigestForTest is the root digest UpsertAgentsMd writes for body.
func VerifiedRootDigestForTest(body jsjson.Value) string { return verifiedRootDigest(body) }
