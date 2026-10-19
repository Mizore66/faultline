//go:build faultline_test

package cli

import "os"

// In test builds FAULTLINE_TEST_SELF_INCIDENT_ROOT replaces the recorded
// self-incident root judge-proof compares with, as the difftest preload
// replaces src/proof-roots.ts RECORDED_SELF_INCIDENT_ROOT for frozen TS: no
// bundle anyone holds has the real root, so this is how the match line is
// compared end to end (difftest TestLiveSelfIncidentMatch).
func init() {
	if root := os.Getenv("FAULTLINE_TEST_SELF_INCIDENT_ROOT"); root != "" {
		recordedSelfIncidentRoot = root
	}
}
