//go:build windows

package term

import (
	"os"
	"syscall"
	"testing"
)

// TestIsTerminalConsole attaches a real console (AllocConsole fails harmlessly
// when the process already has one) and checks the console handles and NUL.
func TestIsTerminalConsole(t *testing.T) {
	syscall.NewLazyDLL("kernel32.dll").NewProc("AllocConsole").Call()
	for _, name := range []string{"CONOUT$", "CONIN$"} {
		f, err := os.OpenFile(name, os.O_RDWR, 0)
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		if !IsTerminal(f) {
			t.Errorf("%s not reported as terminal", name)
		}
		f.Close()
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	if IsTerminal(null) {
		t.Error("NUL (a character device that is not a console) reported as terminal")
	}
}
