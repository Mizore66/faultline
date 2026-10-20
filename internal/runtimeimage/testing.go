//go:build faultline_test

package runtimeimage

// ParseRepoDigestsForTest exposes parseRepoDigests to the live tests.
func ParseRepoDigestsForTest(stdout string) []string { return parseRepoDigests(stdout) }

// NormalizeRepositoryForTest exposes normalizeRepository to the live tests.
func NormalizeRepositoryForTest(repository string) string { return normalizeRepository(repository) }
