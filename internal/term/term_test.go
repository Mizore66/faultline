package term

import (
	"os"
	"testing"
)

func TestIsTerminalRejectsNonTerminals(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "plain")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if IsTerminal(file) {
		t.Fatal("regular file reported as terminal")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if IsTerminal(r) || IsTerminal(w) {
		t.Fatal("pipe reported as terminal")
	}
}

func TestIsTerminalNilFile(t *testing.T) {
	if IsTerminal(nil) {
		t.Fatal("nil file reported as terminal")
	}
}
