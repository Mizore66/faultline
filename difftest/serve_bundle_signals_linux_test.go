//go:build faultline_test && linux && cgo

package difftest

import (
	"debug/buildinfo"
	"os/exec"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/difftest/servefixture"
)

// inheritedSignalDriver is an exec-only setup driver run by the owned
// `python3 -I -c`: it applies the named signal conditions to ITS OWN process
// and then execs the real command, so the target keeps the same PID and the
// same owned process group. No grandchild, sleep or scheduling manipulation.
const inheritedSignalDriver = `
import os, signal, sys
mode, command = sys.argv[1], sys.argv[2:]
if mode in ("ignore", "ignore-blocked"):
    signal.signal(signal.SIGINT, signal.SIG_IGN)
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
if mode in ("blocked", "ignore-blocked"):
    signal.pthread_sigmask(signal.SIG_BLOCK, [signal.SIGINT, signal.SIGTERM])
os.execv(command[0], command)
`

// requireCgoBinary fails (never skips) unless the actual fl under test was
// built with CGO_ENABLED=1, so the evidence below is truly the cgo boot path.
func requireCgoBinary(t *testing.T) {
	t.Helper()
	info, err := buildinfo.ReadFile(flBinary)
	if err != nil {
		t.Fatalf("reading build metadata of %s: %v", flBinary, err)
	}
	for _, setting := range info.Settings {
		if setting.Key == "CGO_ENABLED" {
			if setting.Value != "1" {
				t.Fatalf("flBinary was built with CGO_ENABLED=%s; inherited-signal evidence needs the cgo build", setting.Value)
			}
			return
		}
	}
	t.Fatalf("flBinary build metadata has no CGO_ENABLED setting")
}

// TestLiveServeBundleInheritedSignals starts the concrete Node and Go commands
// with SIGINT/SIGTERM inherited as ignored, blocked, or both, and requires that
// the commands still behave like the frozen Node: an ordinary interrupt ends
// with a completed exit 0, and an interrupt-free terminate dies of a raw
// SIGTERM, with complete matching streams. It catches a Go boot that leaves an
// inherited ignore or mask in place (the signal never delivered, the process
// never stopping) or kills on the wrong signal. This is Linux/cgo evidence
// only: macOS does not implement these inherited resets and this file makes no
// Darwin claim.
func TestLiveServeBundleInheritedSignals(t *testing.T) {
	oracle.Skip(t)
	node, cli := pinServeBundleOracle(t)
	requireCgoBinary(t)
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	corpus := loadServeBundleCorpus(t)
	for _, mode := range []string{"ignore", "blocked", "ignore-blocked"} {
		wrap := func(side liveSide) liveSide {
			return liveSide{
				name: side.name, exe: python,
				prefix: append([]string{"-I", "-c", inheritedSignalDriver, mode, side.exe}, side.prefix...),
			}
		}
		t.Run(mode, func(t *testing.T) {
			t.Run("ordinary-interrupt", func(t *testing.T) {
				p := startSignalPair(t, corpus, node, cli, wrap)
				p.each(t, "interrupt", func(i int, proc *servefixture.Started) error { return proc.Interrupt(stepContext(t), "") })
				ts, tsRaw := p.wait(t, 0)
				goSide, goRaw := p.wait(t, 1)
				for i, r := range []servefixture.ProcessResult{tsRaw, goRaw} {
					if r.Signal != nil || r.ExitCode == nil || *r.ExitCode != 0 {
						t.Errorf("%s %s mode=%s: code=%v signal=%v, want completed exit 0", signalPlatform(), p.sides[i].name, mode, r.ExitCode, r.Signal)
					}
				}
				p.requireAgree(t, ts, goSide)
			})
			t.Run("default-terminate", func(t *testing.T) {
				p := startSignalPair(t, corpus, node, cli, wrap)
				p.each(t, "terminate", func(i int, proc *servefixture.Started) error { return proc.Terminate(stepContext(t)) })
				ts, tsRaw := p.wait(t, 0)
				goSide, goRaw := p.wait(t, 1)
				requireTerminateStatus(t, tsRaw)
				requireTerminateStatus(t, goRaw)
				p.requireAgree(t, ts, goSide)
			})
		})
	}
}
