package difftest

import (
	"encoding/hex"
	"math/rand/v2"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
)

func TestLiveDecodeUTF8(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(9, 10))
	interesting := []byte{0x00, 0x41, 0x7F, 0x80, 0xBF, 0xC0, 0xC2, 0xDF, 0xE0, 0xE1, 0xED, 0xEF, 0xF0, 0xF1, 0xF4, 0xF5, 0xFF, 0xA0, 0x9F, 0x90, 0x8F}
	for i := 0; i < liveCases; i++ {
		b := make([]byte, r.IntN(12))
		for j := range b {
			b[j] = interesting[r.IntN(len(interesting))]
		}
		raw, err := c.Call("decodeUtf8", `{"hex":"`+hex.EncodeToString(b)+`"}`)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := jsjson.Parse(raw)
		if got := nodefs.DecodeUTF8(b); got != want.Str() {
			t.Fatalf("DecodeUTF8(% x) = %q, want %q", b, got, want.Str())
		}
	}
}
