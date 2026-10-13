package difftest

import (
	"os"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
)

// startRootOracle is oracle.Start for the tests CI runs as root. Under
// FAULTLINE_REQUIRE_ROOT it fails where oracle.Start would skip, so the root
// step cannot pass with the oracle off or without root (round 5, §3 P3).
func startRootOracle(t *testing.T) {
	t.Helper()
	if os.Getenv("FAULTLINE_REQUIRE_ROOT") != "" {
		if os.Getenv("FAULTLINE_NODE_ORACLE") != "1" {
			t.Fatal("FAULTLINE_REQUIRE_ROOT is set but FAULTLINE_NODE_ORACLE is not 1")
		}
		if os.Geteuid() != 0 {
			t.Fatal("FAULTLINE_REQUIRE_ROOT is set but the test is not running as root")
		}
	}
	oracle.Start(t).Close() // skip unless FAULTLINE_NODE_ORACLE=1
}
