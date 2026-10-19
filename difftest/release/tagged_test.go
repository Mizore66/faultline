//go:build faultline_test

package release

// tagged: the go job runs every package twice, untagged and with
// -tags faultline_test; the release-build check needs to run only once.
const tagged = true
