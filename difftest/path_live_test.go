package difftest

import (
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// randomPath builds paths from pieces that exercise path.js's root, UNC,
// drive, device, dot and separator handling.
func randomPath(r *rand.Rand) string {
	pieces := []string{"/", `\`, "//", `\\`, ".", "..", "...", "a", "B", "c.d", "C:", "c:", "z:", ":", "con", "CON:", "NUL", "com1:", "COM¹:", "lpt3", "?", "server", "share", "é", "İ", "Ω", "\u212a", "\U0001F600", "\xed\xa0\x80", " ", "x:y", "a:", "PRN:x"}
	var b strings.Builder
	for n := r.IntN(7); n > 0; n-- {
		b.WriteString(pieces[r.IntN(len(pieces))])
	}
	return b.String()
}

func TestLivePath(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(21, 22))
	posixCwds := []string{"/home/u/proj", "/", "/a/b/", "rel/cwd"}
	winCwds := []string{`C:\Users\me\proj`, `c:\`, `D:\x\y\`, `\\srv\share\dir`, `C:/fwd/slash`, `rel`}
	for i := 0; i < liveCases*4; i++ {
		platform := "posix"
		cwd := posixCwds[r.IntN(len(posixCwds))]
		env := map[string]string{}
		if r.IntN(2) == 0 {
			platform = "win32"
			cwd = winCwds[r.IntN(len(winCwds))]
			for n := r.IntN(3); n > 0; n-- {
				key := []string{"=C:", "=D:", "=c:", "=d:", "=Z:"}[r.IntN(5)]
				env[key] = []string{`D:\dcwd`, `C:\wrong`, `d:\lower`, `D:`, `é:\x`, "\u212a:\\k", `Z:\z`, "\xed\xa0\x80:\\s"}[r.IntN(8)]
			}
		}
		fns := []string{"resolve", "normalize", "join", "relative", "isAbsolute", "dirname"}
		fn := fns[r.IntN(len(fns))]
		var args []string
		switch fn {
		case "normalize", "isAbsolute", "dirname":
			args = []string{randomPath(r)}
		case "relative":
			args = []string{randomPath(r), randomPath(r)}
		default:
			for n := r.IntN(4); n > 0; n-- {
				args = append(args, randomPath(r))
			}
		}
		req := jsjson.NewObj()
		req.Set("platform", jsjson.MakeString(platform))
		req.Set("fn", jsjson.MakeString(fn))
		items := make([]jsjson.Value, len(args))
		for j, a := range args {
			items[j] = jsjson.MakeString(a)
		}
		req.Set("args", jsjson.MakeArray(items))
		req.Set("cwd", jsjson.MakeString(cwd))
		envObj := jsjson.NewObj()
		for k, v := range env {
			envObj.Set(k, jsjson.MakeString(v))
		}
		req.Set("env", jsjson.MakeObject(envObj))
		raw, err := c.Call("path", jsjson.Stringify(jsjson.MakeObject(req)))
		if err != nil {
			t.Fatal(err)
		}
		want, err := jsjson.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		cwdFn := func() string { return cwd }
		envFn := func(k string) string { return env[k] }
		var got jsjson.Value
		str := func(s string) { got = jsjson.MakeString(s) }
		switch platform + "." + fn {
		case "posix.resolve":
			str(nodefs.PosixResolve(cwdFn, args...))
		case "posix.normalize":
			str(nodefs.PosixNormalize(args[0]))
		case "posix.join":
			str(nodefs.PosixJoin(args...))
		case "posix.relative":
			str(nodefs.PosixRelative(cwdFn, args[0], args[1]))
		case "posix.isAbsolute":
			got = jsjson.MakeBool(nodefs.PosixIsAbsolute(args[0]))
		case "posix.dirname":
			str(nodefs.PosixDirname(args[0]))
		case "win32.resolve":
			str(nodefs.Win32Resolve(cwdFn, envFn, args...))
		case "win32.normalize":
			str(nodefs.Win32Normalize(args[0]))
		case "win32.join":
			str(nodefs.Win32Join(args...))
		case "win32.relative":
			str(nodefs.Win32Relative(cwdFn, envFn, args[0], args[1]))
		case "win32.isAbsolute":
			got = jsjson.MakeBool(nodefs.Win32IsAbsolute(args[0]))
		case "win32.dirname":
			str(nodefs.Win32Dirname(args[0]))
		}
		if jsjson.Stringify(got) != jsjson.Stringify(want) {
			t.Errorf("%s.%s(%q) cwd=%q env=%v: got %s, want %s", platform, fn, args, cwd, env, jsjson.Stringify(got), jsjson.Stringify(want))
		}
	}
}

// process.cwd() is getcwd's bytes decoded as UTF-8 with replacement; compare
// with the real Node process in a directory whose name is not UTF-8.
func TestLiveCwdMatchesNode(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs a filesystem that accepts arbitrary name bytes")
	}
	oracle.Start(t).Close() // skip unless FAULTLINE_NODE_ORACLE=1
	for _, name := range []string{"plain", "bad\xffdir", "half\xed\xa0\x80", "tail\xe2\x82"} {
		dir := filepath.Join(t.TempDir(), name)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("node", "-e", "process.stdout.write(JSON.stringify(process.cwd()))")
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		want, _ := jsjson.Parse(string(out))
		t.Chdir(dir)
		got, err := nodefs.Cwd()
		if err != nil || got != want.Str() {
			t.Errorf("%q: Cwd() = %q, %v; node %q", name, got, err, want.Str())
		}
	}
}
