package nodeproc

import (
	"slices"
	"testing"
)

// Expectations follow libuv's search_path and search_path_join_test
// (src/win/process.c), traced by hand.
func TestSearchPathWindows(t *testing.T) {
	const cwd = `C:\work`
	for _, tc := range []struct {
		name, path string
		searchCwd  bool
		files      []string
		want       string
	}{
		{"cwd first", `C:\bin`, true, []string{`C:\work\git.exe`, `C:\bin\git.exe`}, `C:\work\git.exe`},
		{"NoDefaultCurrentDirectoryInExePath", `C:\bin`, false, []string{`C:\work\git.exe`, `C:\bin\git.exe`}, `C:\bin\git.exe`},
		{"quoted empty entry is cwd", `"";C:\bin`, false, []string{`C:\work\git.exe`, `C:\bin\git.exe`}, `C:\work\git.exe`},
		{".com before .exe", `C:\bin`, false, []string{`C:\bin\git.exe`, `C:\bin\git.com`}, `C:\bin\git.com`},
		{"same-drive relative", `C:tools`, false, []string{`C:\work\tools\git.exe`}, `C:\work\tools\git.exe`},
		{"same-drive bare", `c:`, false, []string{`C:\work\git.exe`}, `C:\work\git.exe`},
		{"other-drive relative", `D:tools`, false, []string{`D:tools\git.exe`}, `D:tools\git.exe`},
		{"other-drive bare", `D:`, false, []string{`D:git.exe`}, `D:git.exe`},
		{"rooted takes cwd drive", `\bin`, false, []string{`C:\bin\git.exe`}, `C:\bin\git.exe`},
		{"UNC ignores cwd", `\\srv\share\bin`, false, []string{`\\srv\share\bin\git.exe`}, `\\srv\share\bin\git.exe`},
		{"relative joins cwd", `tools`, false, []string{`C:\work\tools\git.exe`}, `C:\work\tools\git.exe`},
		{"quoted with semicolon", `"C:\a;b";C:\bin`, false, []string{`C:\a;b\git.exe`}, `C:\a;b\git.exe`},
		{"trailing separator", `C:\bin\`, false, []string{`C:\bin\git.exe`}, `C:\bin\git.exe`},
		{"not found", `C:\bin`, true, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := searchPath("git", cwd, tc.path, tc.searchCwd, func(p string) bool { return slices.Contains(tc.files, p) })
			if got != tc.want || ok != (tc.want != "") {
				t.Fatalf("got %q %v, want %q", got, ok, tc.want)
			}
		})
	}
}
