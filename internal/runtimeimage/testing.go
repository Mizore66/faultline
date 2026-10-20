//go:build faultline_test

package runtimeimage

import "github.com/Mizore66/faultline/internal/jsjson"

// ParseRepoDigestsForTest exposes parseRepoDigests to the live tests.
func ParseRepoDigestsForTest(stdout string) []string { return parseRepoDigests(stdout) }

// NormalizeRepositoryForTest exposes normalizeRepository to the live tests.
func NormalizeRepositoryForTest(repository string) string { return normalizeRepository(repository) }

// FingerprintForTest runs fingerprintProjectBuildContext on an already
// normalized context and Dockerfile, and returns { files, contextDigest,
// dockerfileDigest, fileCount, totalBytes }.
func FingerprintForTest(contextDirectory, dockerfile string) (jsjson.Value, error) {
	f, err := fingerprintProjectBuildContext(normalizedBuild{contextDirectory: contextDirectory, dockerfile: dockerfile})
	if err != nil {
		return jsjson.Value{}, err
	}
	o := jsjson.NewObj()
	o.Set("files", jsjson.MakeArray(f.files))
	o.Set("contextDigest", jsjson.MakeString(f.contextDigest))
	o.Set("dockerfileDigest", jsjson.MakeString(f.dockerfileDigest))
	o.Set("fileCount", jsjson.MakeNumber(float64(f.fileCount)))
	o.Set("totalBytes", jsjson.MakeNumber(float64(f.totalBytes)))
	return jsjson.MakeObject(o), nil
}
