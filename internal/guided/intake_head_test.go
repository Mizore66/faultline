package guided

import (
	"errors"
	"reflect"
	"runtime"
	"testing"

	"github.com/Mizore66/faultline/internal/nodeproc"
	"github.com/Mizore66/faultline/internal/safegit"
)

func taskHeadStatus(n int) *int { return &n }

type taskHeadCall struct {
	file string
	args []string
	opts nodeproc.SpawnSyncOptions
}

func taskHeadRun(r nodeproc.Result) (string, []string, error, taskHeadCall) {
	var call taskHeadCall
	spawn := func(file string, args []string, o nodeproc.SpawnSyncOptions) nodeproc.Result {
		call = taskHeadCall{file, args, o}
		return r
	}
	head, parents, err := taskLocalHead(spawn, "/repo")
	return head, parents, err, call
}

func TestGuidedHeadArgvEnvAndParse(t *testing.T) {
	out := nodeproc.Result{Status: taskHeadStatus(0), Stdout: []byte(" abc \r\n p1\t p2 \r\n")}
	head, parents, err, call := taskHeadRun(out)
	if err != nil || head != "abc" || !reflect.DeepEqual(parents, []string{"p1", "p2"}) {
		t.Fatalf("%q %q %v", head, parents, err)
	}
	if call.file != "git" {
		t.Fatalf("file %q", call.file)
	}
	want := append(safegit.Intake(), "-C", "/repo", "show", "-s", "--format=%H%n%P", "HEAD")
	if !reflect.DeepEqual(call.args, want) || call.args[1] != "core.hooksPath=/nonexistent/faultline-hooks" {
		t.Fatalf("argv %q", call.args)
	}
	o := call.opts
	if o.Timeout != 0 || o.MaxBuffer != 0 || o.HasInput || o.Cwd != "" || o.WindowsShow {
		t.Fatalf("options %+v", o)
	}
	if runtime.GOOS != "windows" {
		env := o.Env
		tail := []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0",
			"GIT_NO_REPLACE_OBJECTS=1", "GIT_LFS_SKIP_SMUDGE=1", "GIT_ALLOW_PROTOCOL=none"}
		if len(env) != 7 || env[0][:5] != "PATH=" || !reflect.DeepEqual(env[1:], tail) {
			t.Fatalf("env %q", env)
		}
	}
}

func TestGuidedHeadRootHasNoParents(t *testing.T) {
	for _, stdout := range []string{"abc\n\n", "abc"} {
		head, parents, err, _ := taskHeadRun(nodeproc.Result{Status: taskHeadStatus(0), Stdout: []byte(stdout)})
		if err != nil || head != "abc" || parents == nil || len(parents) != 0 {
			t.Fatalf("%q %#v %v", head, parents, err)
		}
	}
}

func TestGuidedHeadFailures(t *testing.T) {
	const prefix = "FaultLine could not read a local Git HEAD for guided intake: "
	const noHead = "FaultLine could not read a local Git HEAD for guided intake; supply --from and --to explicitly."
	cases := []struct {
		name string
		r    nodeproc.Result
		want string
	}{
		{"stderr", nodeproc.Result{Status: taskHeadStatus(128), Stderr: []byte("\n fatal: bad \n")}, prefix + "fatal: bad"},
		{"stderr plus spawn error concatenated", nodeproc.Result{Status: taskHeadStatus(1), Stderr: []byte("oops"), Err: errors.New("spawnSync git ETIMEDOUT")}, prefix + "oopsspawnSync git ETIMEDOUT"},
		{"spawn error only", nodeproc.Result{Err: errors.New("spawnSync git ENOENT")}, prefix + "spawnSync git ENOENT"},
		{"signal without error", nodeproc.Result{Signal: "SIGTERM"}, prefix + "supply --from and --to explicitly"},
		{"blank detail", nodeproc.Result{Status: taskHeadStatus(2), Stderr: []byte("  \n")}, prefix + "supply --from and --to explicitly"},
		{"invalid utf8 replaced", nodeproc.Result{Status: taskHeadStatus(2), Stderr: []byte{'x', 0xff}}, prefix + "x�"},
		{"status zero with error still fails", nodeproc.Result{Status: taskHeadStatus(0), Stdout: []byte("abc\n"), Err: errors.New("spawnSync git ENOBUFS")}, prefix + "spawnSync git ENOBUFS"},
		{"blank head line", nodeproc.Result{Status: taskHeadStatus(0), Stdout: []byte("  \n p\n")}, noHead},
		{"empty output", nodeproc.Result{Status: taskHeadStatus(0)}, noHead},
	}
	for _, c := range cases {
		_, _, err, _ := taskHeadRun(c.r)
		if err == nil || err.Error() != c.want {
			t.Fatalf("%s: %v want %q", c.name, err, c.want)
		}
	}
}
