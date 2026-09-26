package difftest

import "testing"

// The git detail mask hides only git's own stderr; FaultLine's composition
// (the "; " join, spawnSync errors, the exit fallback) is still compared.
func TestMaskGitDetail(t *testing.T) {
	for in, want := range map[string]string{
		"exit 128":             "exit 128",
		"exit null":            "exit null",
		"spawnSync git ENOENT": "spawnSync git ENOENT",
		"fatal: bad object; spawnSync git ENOBUFS":   "<GIT-STDERR>; spawnSync git ENOBUFS",
		"a; b\nc; spawnSync git EACCES":              "<GIT-STDERR>; spawnSync git EACCES",
		"error: not a bundle\nfatal: pack corrupted": "<GIT-STDERR>",
		"exit 128 and more":                          "<GIT-STDERR>",
		"spawnSync git enoent":                       "<GIT-STDERR>",
	} {
		if got := maskGitDetail(in); got != want {
			t.Errorf("maskGitDetail(%q) = %q, want %q", in, got, want)
		}
	}
	out := normalize("- portable Git source verification failed: Git bundle head listing failed: warn; spawnSync git ENOBUFS\n- next\n", "/b")
	if want := "- portable Git source verification failed: Git bundle head listing failed: <GIT-STDERR>; spawnSync git ENOBUFS\n- next\n"; out != want {
		t.Errorf("normalize = %q", out)
	}
}
