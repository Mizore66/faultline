//go:build faultline_test

package clock

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
)

// epochMillis is 2026-07-16T12:00:00.000Z.
const epochMillis = 1784203200000

var state struct {
	mu              sync.Mutex
	init            bool
	seed            string
	tick, uuid, tmp uint64
	rand            uint64
	// offset is FAULTLINE_TEST_CLOCK_OFFSET_MS, added to the epoch: a
	// writer case's later invocations run an hour apart each
	// (difftest/gen/seed.mjs).
	offset int64
}

func load() {
	if !state.init {
		state.seed = os.Getenv("FAULTLINE_TEST_SEED")
		state.offset = 0
		if state.seed != "" {
			state.offset = clockOffset(os.Getenv("FAULTLINE_TEST_CLOCK_OFFSET_MS"))
		}
		state.init = true
	}
}

// init checks FAULTLINE_TEST_CLOCK_OFFSET_MS when the process starts under
// a seed, so a bad value fails every command, also one that never reads
// the clock (seed.mjs checks it when the preload loads).
func init() {
	if os.Getenv("FAULTLINE_TEST_SEED") != "" {
		clockOffset(os.Getenv("FAULTLINE_TEST_CLOCK_OFFSET_MS"))
	}
}

// clockOffset parses FAULTLINE_TEST_CLOCK_OFFSET_MS: unset or empty is 0,
// an integer (-?[0-9]+) is that many milliseconds, and anything else stops
// the process, as seed.mjs does at load.
func clockOffset(raw string) int64 {
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || raw[0] == '+' {
		fmt.Fprintln(os.Stderr, "bad FAULTLINE_TEST_CLOCK_OFFSET_MS")
		os.Exit(1)
	}
	return n
}

func reset(seed string) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.seed, state.init, state.offset = seed, true, 0
	state.tick, state.uuid, state.tmp, state.rand = 0, 0, 0, 0
}

// ResetForTest reseeds the sequence, zeroes every counter and the clock
// offset.
func ResetForTest(seed string) { reset(seed) }

func digest(kind string, n uint64) [32]byte {
	return sha256.Sum256([]byte(state.seed + "\x00" + kind + "\x00" + utoa(n)))
}

func utoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for ; n > 0; n /= 10 {
		i--
		b[i] = byte('0' + n%10)
	}
	return string(b[i:])
}

// NowMillis is Date.now(); Now and NowMillis share one counter.
func NowMillis() int64 {
	state.mu.Lock()
	defer state.mu.Unlock()
	load()
	if state.seed == "" {
		return wallNowMillis()
	}
	ms := epochMillis + state.offset + int64(state.tick)
	state.tick++
	return ms
}

// Now is new Date().
func Now() time.Time {
	state.mu.Lock()
	load()
	seeded := state.seed != ""
	state.mu.Unlock()
	if !seeded {
		return wallNow()
	}
	return time.UnixMilli(NowMillis())
}

// NewUUID is crypto.randomUUID().
func NewUUID() string {
	state.mu.Lock()
	defer state.mu.Unlock()
	load()
	if state.seed == "" {
		return wallNewUUID()
	}
	h := digest("uuid", state.uuid)
	state.uuid++
	return formatUUID(h[:16])
}

// TempSuffix is the six random characters mkdtemp appends.
func TempSuffix() string {
	state.mu.Lock()
	defer state.mu.Unlock()
	load()
	if state.seed == "" {
		return wallTempSuffix()
	}
	h := digest("tmp", state.tmp)
	state.tmp++
	b := make([]byte, 6)
	for i := range b {
		b[i] = tempAlphabet[int(h[i])%len(tempAlphabet)]
	}
	return string(b)
}

// RandRead fills b with random bytes (OpenSSL's RAND_priv_bytes_ex): under
// a seed, SHA-256 blocks of a counter, so a seeded run draws the same
// bytes every time.
func RandRead(b []byte) {
	state.mu.Lock()
	defer state.mu.Unlock()
	load()
	if state.seed == "" {
		wallRandRead(b)
		return
	}
	for i := 0; i < len(b); i += 32 {
		h := digest("rand", state.rand)
		state.rand++
		copy(b[i:], h[:])
	}
}

// ConsumedForTest reports how many time reads (Now and NowMillis share one
// counter) and UUIDs the seeded sequence has handed out; seed.mjs exposes the
// same two counters as globalThis.__faultlineSeedCounters.
func ConsumedForTest() (ticks, uuids int) {
	state.mu.Lock()
	defer state.mu.Unlock()
	return int(state.tick), int(state.uuid)
}
