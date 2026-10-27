package nodeproc

import (
	"runtime"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsexc"
)

// The expectations follow IsWindowsBatchFile's regex
// /\.([a-zA-Z0-9]+)\s*[\.\s]*$/ (Node 22.22.2 src/util-inl.h:666), traced
// by hand, plus every suffix of Node's
// test/parallel/test-child-process-spawn-windows-batch-file.js.
func TestIsWindowsBatchFile(t *testing.T) {
	for _, tc := range []struct {
		file string
		want bool
	}{
		{"a.cmd", true},
		{"A.BAT", true},
		{"x.cmd  ", true},
		{"x.cmd. .", true},
		{"x.cmd.exe", false},
		{"x.cmdx", false},
		{".cmd", true},
		{"x.", false},
		{"x", false},
		{"", false},
		{"cmd", false},
		{"bat", false},
		{"xcmd", false},
		{"x.bat\t\r\n\v\f", true},
		{"x.cmd..", true},
		{"x..cmd", true},
		{"x. cmd", false},     // the capture must follow the dot at once
		{"x.c md", false},     // "md" is not preceded by a dot
		{"x.cmd .exe", false}, // the capture is the last extension
		{"x.exe.cmd", true},
		{"x.cmd1", false},
		{"x.CmD", true},
		{"x.bAt", true},
		{`C:\dir.cmd\codex`, false},
		{`C:\dir.cmd\codex.exe`, false},
		{`C:\Users\me\AppData\Roaming\npm\codex.cmd`, true},
		{`.\codex.CMD `, true},
		{"codex.cmd", true},
		{"codex.exe", false},
		{"codex", false},
		{"x.cmd\u00a0", false}, // NBSP is not \s in the classic locale
		{"x.cmd\x00", true},    // a C string: it ends at the NUL
		{"x.exe\x00.cmd", false},
		{"x.\u00e9cmd", false},
		{"x.ps1", false},
		{"x.com", false},
		{"x.btm", false},
	} {
		if got := isWindowsBatchFile(tc.file); got != tc.want {
			t.Errorf("isWindowsBatchFile(%q) = %v, want %v", tc.file, got, tc.want)
		}
	}
	for _, suffix := range strings.Split("BAT|bAT|BaT|baT|BAt|bAt|Bat|bat|CMD|cMD|CmD|cmD|CMd|cMd|Cmd|cmd|cmd |cmd .|cmd ....", "|") {
		if file := "./nosuchdir/nosuchfile." + suffix; !isWindowsBatchFile(file) {
			t.Errorf("isWindowsBatchFile(%q) = false, want true", file)
		}
	}
	if isWindowsBatchFile("./nosuchdir/nosuchfile") {
		t.Error("isWindowsBatchFile(./nosuchdir/nosuchfile) = true")
	}
}

// TestBatchFileRefusal: on Windows both spawn paths refuse a batch file
// before looking for it, as Node does; elsewhere the name is spawned (and
// not found).
func TestBatchFileRefusal(t *testing.T) {
	file := `.\faultline-no-such-dir\nosuchfile.CMD .`
	sync, async := "spawnSync "+file+" EINVAL", "spawn EINVAL"
	if runtime.GOOS != "windows" {
		file = strings.ReplaceAll(file, `\`, "/")
		sync, async = "spawnSync "+file+" ENOENT", "spawn "+file+" ENOENT"
	}
	r := SpawnSync(file, nil, 0)
	if r.Err == nil || r.Err.Error() != sync || r.Status != nil || r.Stdout != nil {
		t.Fatalf("SpawnSync: %+v, want %s", r, sync)
	}
	var got string
	if err := jsexc.Try(func() {
		if exit := Spawn(file, nil, SpawnOptions{}).Wait(); exit.Err != nil {
			got = exit.Err.Error()
		}
	}); err != nil {
		got = err.Error()
	}
	if got != async {
		t.Fatalf("Spawn: %q, want %q", got, async)
	}
}
