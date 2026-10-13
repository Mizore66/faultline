package difftest

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/clock"
)

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

// The probe reads the clock through the same bindings dist uses.
const seedProbe = `
import { randomUUID } from "node:crypto";
import { mkdtempSync, rmSync } from "node:fs";
import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
const a = mkdtempSync(join(tmpdir(), "seed-probe-"));
const b = await mkdtemp(join(tmpdir(), "seed-probe-"));
rmSync(a, { recursive: true }); rmSync(b, { recursive: true });
console.log([new Date().toISOString(), Date.now(), new Date(5).toISOString(), randomUUID(), randomUUID(),
  a.slice(-6), b.slice(-6)].join("\n"));
`

// TestLiveSeedPreload runs seed.mjs under Node and compares its sequence with
// the seeded Go one.
func TestLiveSeedPreload(t *testing.T) {
	oracle.Skip(t)
	repo := oracle.RepoRoot(t)
	cmd := exec.Command("node", "--import", repo+"/difftest/gen/seed.mjs", "--input-type=module", "-e", seedProbe)
	cmd.Env = append(cmd.Environ(), "FAULTLINE_TEST_SEED=slice2")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	clock.ResetForTest("slice2")
	want := strings.Join([]string{clock.ISO(clock.Now()), itoa(clock.NowMillis()), "1970-01-01T00:00:00.005Z",
		clock.NewUUID(), clock.NewUUID(), clock.TempSuffix(), clock.TempSuffix()}, "\n") + "\n"
	if string(out) != want {
		t.Fatalf("preload:\n%s\ngo:\n%s", out, want)
	}
}
