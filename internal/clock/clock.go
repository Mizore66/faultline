// Package clock is the one place the CLI reads wall time, UUIDs and temp-name
// randomness, so writer goldens can run against a seeded sequence that the
// frozen TS side reproduces (difftest/gen/seed.mjs). Release builds never
// consult FAULTLINE_TEST_SEED.
package clock

import (
	crand "crypto/rand"
	"fmt"
	"math/rand/v2"
	"time"
)

// tempAlphabet is libuv's mkdtemp alphabet.
const tempAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// ISO is Date.prototype.toISOString (UTC, milliseconds). Years outside
// 0-9999 are unreachable here.
func ISO(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

func wallNow() time.Time { return time.Now() }

func wallNowMillis() int64 { return time.Now().UnixMilli() }

// formatUUID sets the v4 version and variant bits and prints b as 8-4-4-4-12.
func formatUUID(b []byte) string {
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func wallNewUUID() string {
	b := make([]byte, 16)
	if _, err := crand.Read(b); err != nil {
		panic(err)
	}
	return formatUUID(b)
}

func wallTempSuffix() string {
	b := make([]byte, 6)
	for i := range b {
		b[i] = tempAlphabet[rand.IntN(len(tempAlphabet))]
	}
	return string(b)
}
