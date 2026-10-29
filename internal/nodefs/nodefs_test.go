package nodefs

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestDecodeUTF8MatchesNode(t *testing.T) {
	cases := []struct {
		in   []byte
		want string
	}{
		{[]byte{0xE2, 0x82}, "\ufffd"},
		{[]byte{0xF0, 0x9F, 0x98}, "\ufffd"},
		{[]byte{0xC0, 0xAF}, "\ufffd\ufffd"},
		{[]byte{0xED, 0xA0, 0x80}, "\ufffd\ufffd\ufffd"},
		{[]byte{0xF4, 0x90, 0x80, 0x80}, "\ufffd\ufffd\ufffd\ufffd"},
		{[]byte{0xE2, 0x82, 0x41}, "\ufffdA"},
		{[]byte{0xFF}, "\ufffd"},
		{[]byte{0xEF, 0xBB, 0xBF, 0x41}, "\ufeffA"},
		{[]byte{0xF0, 0x9F, 0x98, 0x80}, "\U0001F600"},
		{[]byte{0xE0, 0x80, 0x80}, "\ufffd\ufffd\ufffd"},
		{[]byte{0x61, 0xF1, 0x80, 0x80, 0xE1, 0x80, 0xC2, 0x62}, "a\ufffd\ufffd\ufffdb"},
	}
	for _, c := range cases {
		if got := DecodeUTF8(c.in); got != c.want {
			t.Errorf("DecodeUTF8(% x) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNodeErrorText(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.json")
	if _, err := ReadText(missing); err == nil || err.Error() != "ENOENT: no such file or directory, open '"+missing+"'" {
		t.Fatalf("ReadText error = %v", err)
	}
	if _, err := ReadBytes(dir); err == nil || err.Error() != "EISDIR: illegal operation on a directory, read" {
		t.Fatalf("ReadBytes(dir) error = %v", err)
	}
	if _, err := Lstat(missing); err == nil || err.Error() != "ENOENT: no such file or directory, lstat '"+missing+"'" {
		t.Fatalf("Lstat error = %v", err)
	}
	if _, err := ReadDirNames(missing); err == nil || err.Error() != "ENOENT: no such file or directory, scandir '"+missing+"'" {
		t.Fatalf("ReadDirNames error = %v", err)
	}
	file := filepath.Join(dir, "f")
	os.WriteFile(file, nil, 0o600)
	if _, err := ReadText(filepath.Join(file, "x")); err == nil || err.Error() != "ENOTDIR: not a directory, open '"+filepath.Join(file, "x")+"'" {
		if runtime.GOOS != "windows" {
			t.Fatalf("ENOTDIR error = %v", err)
		}
	}
}

func TestReadDirNamesIsByteSorted(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"b", "Z", "_", "1", "é"} {
		os.WriteFile(filepath.Join(dir, n), nil, 0o600)
	}
	got, _ := ReadDirNames(dir)
	want := []string{"1", "Z", "_", "b", "é"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ReadDirNames = %q", got)
		}
	}
}

func TestRelative(t *testing.T) {
	if Relative("/a/b", "/a/b") != "" {
		t.Fatal(`Relative of equal paths must be ""`)
	}
	if runtime.GOOS != "windows" && Relative("/a/b", "/a/c/d") != "../c/d" {
		t.Fatal("Relative")
	}
}

// A name Windows rejects outright (ERROR_INVALID_NAME) must still read as
// ENOENT, as Node reports it there and as Linux reports it for the same name.
func TestInvalidWindowsNameIsENOENT(t *testing.T) {
	path := filepath.Join(t.TempDir(), "frozen.json\v")
	if _, err := Lstat(path); err == nil || err.Error() != "ENOENT: no such file or directory, lstat '"+path+"'" {
		t.Fatalf("Lstat error = %v", err)
	}
	if _, err := ReadText(path); err == nil || err.Error() != "ENOENT: no such file or directory, open '"+path+"'" {
		t.Fatalf("ReadText error = %v", err)
	}
}

// process.cwd() is the physical directory (getcwd), not the shell's $PWD.
func TestResolveUsesPhysicalCwd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinked cwd is a POSIX shell concern")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	phys := filepath.Join(dir, "phys", "sub")
	if err := os.MkdirAll(phys, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "logical", "link")
	os.MkdirAll(filepath.Dir(link), 0o755)
	if err := os.Symlink(filepath.Join("..", "phys", "sub"), link); err != nil {
		t.Fatal(err)
	}
	t.Chdir(link)
	t.Setenv("PWD", link)
	if got, want := Resolve("../bundle"), filepath.Join(dir, "phys", "bundle"); got != want {
		t.Fatalf("Resolve = %q, want %q", got, want)
	}
}

// readdirSync decodes each raw name as UTF-8, so a name with invalid bytes
// comes back with U+FFFD and no longer names the file.
func TestReadDirNamesDecodesLikeNode(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs a filesystem that accepts arbitrary name bytes")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad\xffname"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	names, err := ReadDirNames(dir)
	if err != nil || len(names) != 1 || names[0] != "bad�name" {
		t.Fatalf("%q %v", names, err)
	}
	_, err = Lstat(Join(dir, names[0]))
	if err == nil || err.Error() != "ENOENT: no such file or directory, lstat '"+Join(dir, "bad�name")+"'" {
		t.Fatalf("lstat: %v", err)
	}
}

func TestReadSizeLimitsMatchNode(t *testing.T) {
	dir := t.TempDir()
	huge := filepath.Join(dir, "huge")
	f, err := os.Create(huge)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(1 << 31); err != nil { // sparse
		t.Skip(err)
	}
	f.Close()
	if _, err := ReadBytes(huge); err == nil || err.Error() != "File size (2147483648) is greater than 2 GiB" {
		t.Fatalf("ReadBytes: %v", err)
	}
	if _, err := ReadText(huge); err != ErrStringTooLong {
		t.Fatalf("ReadText: %v", err)
	}
	if testing.Short() {
		return
	}
	// Node 22 compares the raw byte count with >=, before any decoding: one
	// byte below the limit reads, the limit itself and multi-byte text of that
	// many bytes (far fewer UTF-16 units) do not.
	for _, tc := range []struct {
		size int64
		ok   bool
	}{{maxStringLength - 1, true}, {maxStringLength, false}} {
		f := filepath.Join(dir, "edge")
		if err := os.Truncate(f, 0); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		fh, err := os.Create(f)
		if err != nil {
			t.Fatal(err)
		}
		fh.Truncate(tc.size)
		fh.Close()
		if _, err := ReadText(f); (err == nil) != tc.ok {
			t.Fatalf("ReadText(%d bytes): %v", tc.size, err)
		}
	}
}

// A removed cwd is Node's uv_cwd error, on darwin too (where Go's getcwd
// wrapper misses libc's NULL return and a stale path must be rejected).
func TestCwdRemovedDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot remove the current directory")
	}
	dir := filepath.Join(t.TempDir(), "gone")
	os.Mkdir(dir, 0o755)
	t.Chdir(dir)
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := Cwd(); err == nil || err.Error() != "ENOENT: no such file or directory, uv_cwd" {
		t.Fatalf("Cwd() error = %v", err)
	}
	os.Mkdir(dir, 0o755) // recreated: still not the directory we are in
	if _, err := Cwd(); err == nil || err.Error() != "ENOENT: no such file or directory, uv_cwd" {
		t.Fatalf("Cwd() after recreate error = %v", err)
	}
}

// Node throws fs and uv_cwd errors from C++ (UVException): an errno libuv
// has no name for prints as "Unknown system error -N" for both the code and
// the description.
func TestUnnamedErrno(t *testing.T) {
	if isWindows {
		t.Skip("Windows maps unknown errors to UNKNOWN")
	}
	errno := syscall.Errno(4095) // no libuv name on any Unix
	err := wrap(errno, "mkdtemp", "/tmp/x-XXXXXX")
	want := "Unknown system error -4095: Unknown system error -4095, mkdtemp '/tmp/x-XXXXXX'"
	if err.Error() != want {
		t.Errorf("got %q, want %q", err.Error(), want)
	}
	if got := readErr(errno).Error(); got != "Unknown system error -4095: Unknown system error -4095, read" {
		t.Errorf("read error: %q", got)
	}
}

// libuv's uv__fs_get_dirent_type names seven d_types; Node lstats every
// other entry (round 5, row 10).
func TestDirentTypeKnown(t *testing.T) {
	known := map[uint8]bool{1: true, 2: true, 4: true, 6: true, 8: true, 10: true, 12: true}
	for d := 0; d < 256; d++ {
		if got := direntTypeKnown(uint8(d)); got != known[uint8(d)] {
			t.Errorf("direntTypeKnown(%d) = %v", d, got)
		}
	}
}

// TestErrorPathReplacesLoneSurrogate pins Node 22's text: fs errors print
// the path through UTF-8, so a lone surrogate shows as U+FFFD.
func TestErrorPathReplacesLoneSurrogate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows prints the namespaced path (errorPath's Windows branch)")
	}
	dir := filepath.Join(t.TempDir(), "missing")
	path, shown := dir+"/\xed\xb0\x80", dir+"/�"
	if _, err := ReadText(path); err == nil || err.Error() != "ENOENT: no such file or directory, open '"+shown+"'" {
		t.Errorf("ReadText error = %v", err)
	}
	if err := WriteFileUTF8(path, "x", 0o600, false); err == nil || err.Error() != "ENOENT: no such file or directory, open '"+shown+"'" {
		t.Errorf("WriteFileUTF8 error = %v", err)
	}
	if err := Rename(path, path+"b"); err == nil || err.Error() != "ENOENT: no such file or directory, rename '"+shown+"' -> '"+shown+"b'" {
		t.Errorf("Rename error = %v", err)
	}
	if err := Mkdir(path, 0o700); err == nil || err.Error() != "ENOENT: no such file or directory, mkdir '"+shown+"'" {
		t.Errorf("Mkdir error = %v", err)
	}
}

// mkdtempSync names the template in its error, fs/promises.mkdtemp the path
// it tried (req->path after the C library's mkdtemp filled it in); Windows
// libuv prints the template for both (round 7 #3).
func TestMkdtempErrorPath(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "missing", "faultline-x-")
	_, err := Mkdtemp(prefix)
	if want := "ENOENT: no such file or directory, mkdtemp '" + errorPath(prefix) + "XXXXXX'"; err == nil || err.Error() != want {
		t.Errorf("Mkdtemp: %v, want %s", err, want)
	}
	_, err = MkdtempAsync(prefix)
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("MkdtempAsync: %v", err)
	}
	suffix, found := strings.CutPrefix(e.Path, errorPath(prefix))
	if isWindows {
		if suffix != "XXXXXX" {
			t.Errorf("MkdtempAsync on Windows: %v", err)
		}
		return
	}
	if !found || len(suffix) != 6 || suffix == "XXXXXX" || strings.Trim(suffix, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") != "" || e.Code != "ENOENT" {
		t.Errorf("MkdtempAsync: %v", err)
	}
}

// TestMkdirAllFollowsSymlinks pins MKDirpSync's uv_fs_stat (not lstat) of
// an existing path: a symlink to a directory counts as the directory
// (r9-3 F2). Expected results from Node 22's mkdirSync({recursive: true}).
func TestMkdirAllFollowsSymlinks(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"real", "file", "link", "flink", "dangle", "link2"} {
		var err error
		switch p {
		case "real":
			err = os.Mkdir(filepath.Join(dir, p), 0o755)
		case "file":
			err = os.WriteFile(filepath.Join(dir, p), nil, 0o644)
		case "link":
			err = os.Symlink("real", filepath.Join(dir, p))
		case "flink":
			err = os.Symlink("file", filepath.Join(dir, p))
		case "dangle":
			err = os.Symlink("nowhere", filepath.Join(dir, p))
		case "link2":
			err = os.Symlink("link", filepath.Join(dir, p))
		}
		if err != nil {
			if runtime.GOOS == "windows" {
				// CI's Windows runners are administrators, who may create
				// symlinks; elsewhere that needs Developer Mode.
				t.Skip("creating a symlink: " + err.Error())
			}
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ path, want string }{
		{"link", ""},
		{"link/sub", ""},
		{"link2", ""},
		{"link2/a/b", ""},
		{"flink", "EEXIST: file already exists, mkdir '%s'"},
		{"flink/x", "ENOTDIR: not a directory, mkdir '%s'"},
		{"dangle", "ENOENT: no such file or directory, mkdir '%s'"},
		{"dangle/x", "ENOENT: no such file or directory, mkdir '%s'"},
	} {
		if runtime.GOOS == "windows" && c.want != "" {
			// The error texts were measured on Unix only; on Windows
			// (CI's runners can create symlinks) only the links to
			// directories, which F2 is about, are checked.
			continue
		}
		p := filepath.Join(dir, filepath.FromSlash(c.path))
		err := MkdirAll(p, 0o755)
		got := ""
		if err != nil {
			got = err.Error()
		}
		want := c.want
		if want != "" {
			want = strings.Replace(want, "%s", p, 1)
		}
		if got != want {
			t.Errorf("MkdirAll(%s) = %q, want %q", c.path, got, want)
		}
	}
	if info, err := os.Stat(filepath.Join(dir, "real", "a", "b")); err != nil || !info.IsDir() {
		t.Errorf("link2/a/b was not created through the links: %v", err)
	}
}

// writeFileSync on a directory: "w" is EISDIR and "wx" EEXIST, as libuv's
// fs__open reports them on Windows (ERROR_FILE_EXISTS with O_CREAT and
// without O_EXCL is UV_EISDIR) and open(2) on Unix. On Windows Go's
// syscall.Open invents syscall.EISDIR for both, which used to print UNKNOWN.
func TestWriteOpenOnDirectory(t *testing.T) {
	dir := t.TempDir()
	if got, want := WriteFileUTF8(dir, "x", 0o666, false), "EISDIR: illegal operation on a directory, open '"+errorPath(dir)+"'"; got == nil || got.Error() != want {
		t.Errorf("w: got %v, want %q", got, want)
	}
	if got, want := WriteFileUTF8(dir, "x", 0o666, true), "EEXIST: file already exists, open '"+errorPath(dir)+"'"; got == nil || got.Error() != want {
		t.Errorf("wx: got %v, want %q", got, want)
	}
	if got, want := WriteFileSyncUTF8(dir, "x", 0o600), "EEXIST: file already exists, open '"+errorPath(dir)+"'"; got == nil || got.Error() != want {
		t.Errorf("WriteFileSyncUTF8: got %v, want %q", got, want)
	}
}

// Go's invented Windows syscall.EISDIR maps by the open's flags (EEXIST for
// wx). On Unix syscall.EISDIR is the kernel's own errno (Linux reports it
// for wx only on a path ending in "/"; a plain directory gives EEXIST) and
// stays EISDIR for w and wx alike.
func TestOpenWriteErrMapsGoEISDIR(t *testing.T) {
	err := &os.PathError{Op: "open", Path: "d", Err: syscall.EISDIR}
	if got := openWriteErr(err, "/d", false).(*Error).Code; got != "EISDIR" {
		t.Errorf("w: %s", got)
	}
	wantWx := openWriteCode(true, runtime.GOOS)
	if got := openWriteErr(err, "/d", true).(*Error).Code; got != wantWx {
		t.Errorf("wx: %s, want %s", got, wantWx)
	}
	if got := codeOf(syscall.EISDIR); got != "EISDIR" {
		t.Errorf("codeOf(syscall.EISDIR) = %s", got)
	}
	if got := openWriteErr(syscall.ENOENT, "/d", true).(*Error).Code; got != "ENOENT" {
		t.Errorf("ENOENT: %s", got)
	}
}

func TestOpenWriteCodeByPlatform(t *testing.T) {
	cases := []struct {
		exclusive  bool
		goos, want string
	}{
		{false, "windows", "EISDIR"}, {true, "windows", "EEXIST"},
		{false, "linux", "EISDIR"}, {true, "linux", "EISDIR"},
		{true, "darwin", "EISDIR"},
	}
	for _, c := range cases {
		if got := openWriteCode(c.exclusive, c.goos); got != c.want {
			t.Errorf("%v %s: %s, want %s", c.exclusive, c.goos, got, c.want)
		}
	}
}

// ReadDirTypes is readdirSync withFileTypes: byte order, and a symlink is
// neither a directory nor a file.
func TestReadDirTypes(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "b"), nil, 0o644)
	os.Mkdir(filepath.Join(dir, "a"), 0o755)
	if err := os.Symlink("b", filepath.Join(dir, "c")); err != nil {
		t.Skip(err)
	}
	got, err := ReadDirTypes(dir)
	want := []Dirent{{Name: "a", IsDir: true}, {Name: "b", IsFile: true}, {Name: "c"}}
	if err != nil || len(got) != len(want) {
		t.Fatalf("%v %v", got, err)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ReadDirTypes = %v", got)
		}
	}
	missing := filepath.Join(dir, "missing")
	if _, err := ReadDirTypes(missing); err == nil || err.Error() != "ENOENT: no such file or directory, scandir '"+missing+"'" {
		t.Fatalf("ReadDirTypes error = %v", err)
	}
}
