package nativefixture

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

func TestNativeRawBlobExactBytesAndFreshness(t *testing.T) {
	data := []byte{0, 0xff, '\n'}
	sum := sha256.Sum256(data)
	want := fmt.Sprintf(`{"encoding":"base64","data":"AP8K","sha256":"sha256:%x","complete":true}`, sum)
	got := RawBlob(data, true)
	data[0] = 1
	if text := jsjson.Stringify(got); text != want {
		t.Fatalf("blob=%s want=%s", text, want)
	}
	got.Obj().Set("data", jsjson.MakeString("changed"))
	if text := jsjson.Stringify(RawBlob([]byte{0, 0xff, '\n'}, true)); text != want {
		t.Fatalf("later blob=%s want=%s", text, want)
	}
}

func TestNativeRawBlobEmptyAndIncomplete(t *testing.T) {
	for _, tc := range []struct {
		name     string
		data     []byte
		complete bool
		want     string
	}{
		{"nil", nil, true, `{"encoding":"base64","data":"","sha256":"sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","complete":true}`},
		{"empty", []byte{}, true, `{"encoding":"base64","data":"","sha256":"sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","complete":true}`},
		{"incomplete", []byte{}, false, `{"encoding":"base64","data":"","sha256":"sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","complete":false}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if text := jsjson.Stringify(RawBlob(tc.data, tc.complete)); text != tc.want {
				t.Fatalf("blob=%s want=%s", text, tc.want)
			}
		})
	}
}
