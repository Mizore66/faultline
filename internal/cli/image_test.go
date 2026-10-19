package cli

import (
	"strings"
	"testing"
)

func TestRequireImageOrConfig(t *testing.T) {
	repo := t.TempDir()
	if image, err := requireImageOrConfig(repo, []string{"--image", "x"}); err != nil || image != "x" {
		t.Fatal(image, err)
	}
	_, err := requireImageOrConfig(repo, nil)
	if err == nil || !strings.HasPrefix(err.Error(), "Missing required image: pass --image ") {
		t.Fatal(err)
	}
}
