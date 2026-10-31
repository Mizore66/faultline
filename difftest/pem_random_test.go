//go:build faultline_test

package difftest

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/witness"
)

// The exact public input from the Node 22.22.2 / OpenSSL 3.5.5 live failure.
// Both complete stacks were independently observed in a fixed 100000-parse
// Node sample: 99987 common, 13 rare. No other input inherits this contract.
const pemRandomBinaryKey = "-----BEGIN PUBLIC KEY-----\nMIG/MIGOBgcqhkjOPQIBMIGCAgEBMB4GByqGSM49AQIwEwICAKMGCSqGSM49AQID\nAgICAIowLgQVBPi/iHtTjhAP4D26ALYXSwzziTxPBBUAaNZE6fusw2ZO7btvM3mw\nSF9aNB8EFgMEYxCUloabcv8LkyMIJ3FB5iUhO2oCFQQAAAAAAAAAAAAAAAAAAAAA\nAAAAAQMsAAQEYxCUloabcv8LkyMIJ3FB5iUhO2oCHTZSYV5ibcI5TcbLURVG/xTv\nURk=\n-----END PUBLIC KEY-----\n"

const (
	pemRandomCommon = `{"ok":false,"message":"error:03000072:digital envelope routines::decode error","stack":["error:08080010:elliptic curve routines::EC lib","error:08080010:elliptic curve routines::EC lib","error:0800006E:elliptic curve routines::invalid compressed point","error:03000072:digital envelope routines::decode error"]}`
	pemRandomRare   = `{"ok":false,"message":"error:03000072:digital envelope routines::decode error","stack":["error:08080010:elliptic curve routines::EC lib","error:08080010:elliptic curve routines::EC lib","error:03000072:digital envelope routines::decode error"]}`
)

// Catches the false implication that absence in 4200 Node trials means an
// outcome is impossible. The count maps are the complete failed CI samples,
// not mocked parsers; they exercise the live test's actual decision function.
func TestPEMRepeatedKnownRandom(t *testing.T) {
	c := pemCase{Text: pemRandomBinaryKey, Pub: true}
	for _, tc := range []struct {
		name         string
		node, goSide map[string]int
	}{
		{"failed-CI", map[string]int{pemRandomCommon: 4200}, map[string]int{pemRandomCommon: 199, pemRandomRare: 1}},
		{"reverse", map[string]int{pemRandomCommon: 199, pemRandomRare: 1}, map[string]int{pemRandomCommon: 4200}},
		{"both-unanimous", map[string]int{pemRandomCommon: 200}, map[string]int{pemRandomCommon: 200}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if why := pemRepeatedDifference(c, tc.node, tc.goSide); why != "" {
				t.Fatalf("oracle-proven random outcomes rejected: %s", why)
			}
		})
	}
}

// Catches a global noise allowance, shortened-stack comparison, or a rate
// exemption that would accept an implementation returning the rare stack often.
func TestPEMRepeatedRejectsUnknownAndWrongRate(t *testing.T) {
	c := pemCase{Text: pemRandomBinaryKey, Pub: true}
	common := map[string]int{pemRandomCommon: 200}
	unknown := strings.Replace(pemRandomCommon, "decode error", "different error", 1)
	for _, tc := range []struct {
		name         string
		node, goSide map[string]int
	}{
		{"same-unknown-both", map[string]int{unknown: 200}, map[string]int{unknown: 200}},
		{"unknown-go", common, map[string]int{pemRandomCommon: 199, unknown: 1}},
		{"unknown-node", map[string]int{pemRandomCommon: 199, unknown: 1}, common},
		{"wrong-rate-go", common, map[string]int{pemRandomCommon: 195, pemRandomRare: 5}},
		{"wrong-rate-node", map[string]int{pemRandomCommon: 195, pemRandomRare: 5}, common},
		{"same-wrong-rate-both", map[string]int{pemRandomCommon: 195, pemRandomRare: 5}, map[string]int{pemRandomCommon: 195, pemRandomRare: 5}},
		{"all-rare-both", map[string]int{pemRandomRare: 200}, map[string]int{pemRandomRare: 200}},
		{"empty-node", map[string]int{}, common},
		{"empty-go", common, map[string]int{}},
		{"negative-count", map[string]int{pemRandomCommon: 201, pemRandomRare: -1}, common},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if why := pemRepeatedDifference(c, tc.node, tc.goSide); why == "" {
				t.Fatal("invalid full outcome or frequency accepted")
			}
		})
	}
}

// Catches widening eligibility to private parsing, sibling keys, or all
// reducible fields. The original exact/unanimity and shares rules still apply.
func TestPEMRepeatedStrictDefault(t *testing.T) {
	for _, c := range []pemCase{
		{Text: pemRandomBinaryKey, Pub: false},
		{Text: pemRandomBinaryKey + "\n", Pub: true},
		{Text: "unrelated", Pub: true},
	} {
		if why := pemRepeatedDifference(c, map[string]int{pemRandomCommon: 4200}, map[string]int{pemRandomCommon: 199, pemRandomRare: 1}); why == "" {
			t.Fatalf("unlisted input lost strict unanimity (pub=%v): %q", c.Pub, c.Text)
		}
		if why := pemRepeatedDifference(c, map[string]int{pemRandomCommon: 200}, map[string]int{pemRandomCommon: 200}); why != "" {
			t.Fatalf("identical deterministic counts rejected: %s", why)
		}
		if why := pemRepeatedDifference(c, map[string]int{"a": 100, "b": 100}, map[string]int{"a": 100, "b": 100}); why != "" {
			t.Fatalf("matching random shares rejected: %s", why)
		}
		if why := pemRepeatedDifference(c, map[string]int{"a": 199, "b": 1}, map[string]int{"a": 100, "b": 100}); why == "" {
			t.Fatal("different random shares accepted")
		}
	}
}

// Catches removing inverse blinding or retaining an error from one of the two
// decoder attempts whose queues should be popped. These seeds were classified
// independently against the factored modulus, not searched during the test.
func TestPEMRandomSeededStacks(t *testing.T) {
	witness.SetOpenSSLForTest("3.5.5")
	t.Cleanup(func() { witness.SetOpenSSLForTest("3.5"); clock.ResetForTest("") })
	modulus := new(big.Int).SetBit(new(big.Int), 163, 1)
	modulus.SetBit(modulus, 138, 1).SetBit(modulus, 0, 1)
	for _, tc := range []struct{ seed, pattern, want string }{
		{"pem-probe-0", "UUU", pemRandomCommon},
		{"pem-probe-21350", "NUU", pemRandomCommon},
		{"pem-probe-4654", "UNU", pemRandomCommon},
		{"pem-probe-14324", "UUN", pemRandomRare},
	} {
		t.Run(tc.seed, func(t *testing.T) {
			for i := range 3 {
				hash := sha256.Sum256([]byte(fmt.Sprintf("%s\x00rand\x00%d", tc.seed, i)))
				raw := append([]byte(nil), hash[:21]...)
				raw[0] &= 7
				value := new(big.Int).SetBytes(raw)
				unit := pemPolynomialGCD(value, modulus).Cmp(big.NewInt(1)) == 0
				if value.Sign() == 0 || unit != (tc.pattern[i] == 'U') {
					t.Fatalf("seed no longer has blinding pattern %s at draw %d", tc.pattern, i)
				}
			}
			clock.ResetForTest(tc.seed)
			if got := pemOutcome(witness.ParseKeyForTest(pemRandomBinaryKey, true)); got != tc.want {
				t.Fatalf("full stack differs:\n got %s\nwant %s", got, tc.want)
			}
			next := make([]byte, 21)
			clock.RandRead(next)
			expected := sha256.Sum256([]byte(tc.seed + "\x00rand\x003"))
			if !bytes.Equal(next, expected[:21]) {
				t.Fatal("parse did not consume exactly three blinding draws")
			}
		})
	}
}
