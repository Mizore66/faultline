//go:build !faultline_test

package clock

import "time"

// Now is new Date().
func Now() time.Time { return wallNow() }

// NowMillis is Date.now().
func NowMillis() int64 { return wallNowMillis() }

// NewUUID is crypto.randomUUID().
func NewUUID() string { return wallNewUUID() }

// TempSuffix is the six random characters mkdtemp appends.
func TempSuffix() string { return wallTempSuffix() }

// RandRead fills b with random bytes: OpenSSL's RAND_priv_bytes_ex, which
// the key-parsing port's blinding, random searches and ML-KEM pairwise
// test message draw from.
func RandRead(b []byte) { wallRandRead(b) }
