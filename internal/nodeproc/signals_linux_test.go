//go:build linux && !(mips || mipsle || mips64 || mips64le)

package nodeproc

import "testing"

// TestSpawnSignalNamesLinux pins the Linux-only names of Node's
// signo_string a child can die of (r9-3 N4): SIGPWR and SIGIO are named
// (Node 22.22.2: code null, signal "SIGPWR"/"SIGIO").
func TestSpawnSignalNamesLinux(t *testing.T) {
	for _, sig := range []string{"PWR", "IO", "SYS"} {
		exit := Spawn("/bin/sh", []string{"-c", "kill -" + sig + " $$"}, SpawnOptions{}).Wait()
		if exit.Code != nil || exit.Signal != "SIG"+sig {
			t.Errorf("SIG%s: %+v", sig, exit)
		}
	}
}
