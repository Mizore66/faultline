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
