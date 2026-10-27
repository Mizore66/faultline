// Package jsdate answers the one Date question the attestation schemas ask:
// does `new Date(s).toISOString()` give s back? It is a direct check of the
// fields, not a port of V8's Date parser.
package jsdate

import (
	"regexp"
	"strconv"
)

// isoShape is every string Date.prototype.toISOString can return: a
// four-digit year for 0000-9999, otherwise a signed six-digit year.
var isoShape = regexp.MustCompile(`^([+-]\d{6}|\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})\.(\d{3})Z$`)

// maxTimeMs is ECMAScript's time value limit (±8.64e15 ms from the epoch).
const maxTimeMs = 8_640_000_000_000_000

// IsCanonicalISO is `Number.isFinite(new Date(s).getTime()) &&
// new Date(s).toISOString() === s` (the TimestampSchema refine of
// src/attestation.ts:38-42). toISOString only ever prints isoShape, so any
// other string is not canonical; a string of that shape round-trips only
// when its fields need no rollover and its year uses the form toISOString
// chooses. Measured on Node 22.22.2:
//
//   - 2026-02-30T00:00:00.000Z parses to 2026-03-02 (not canonical);
//   - 2026-01-01T24:00:00.000Z parses to the next day (not canonical);
//   - …T23:59:60… and …T25:… are NaN.
//
// So canonical means month 01-12, a day within the month (leap years by the
// proleptic Gregorian rule, year 0000 included), hour 00-23 and minute and
// second 00-59, with the time value within ±8.64e15 ms. "-000000" is not a
// valid year, and toISOString writes years 0-9999 with four digits.
func IsCanonicalISO(s string) bool {
	m := isoShape.FindStringSubmatch(s)
	if m == nil || m[1] == "-000000" {
		return false
	}
	n := func(i int) int64 { v, _ := strconv.ParseInt(m[i], 10, 64); return v }
	year := n(1)
	if len(m[1]) == 7 && year >= 0 && year <= 9999 {
		return false
	}
	month, day, hour, minute, second := n(2), n(3), n(4), n(5), n(6)
	if month < 1 || month > 12 || day < 1 || day > daysInMonth(year, month) || hour > 23 || minute > 59 || second > 59 {
		return false
	}
	t := daysFromCivil(year, month, day)*86_400_000 + ((hour*60+minute)*60+second)*1000 + n(7)
	return t >= -maxTimeMs && t <= maxTimeMs
}

// TimeValue is Date.parse(s) for a string that IsCanonicalISO accepts: its
// time value in milliseconds since the epoch. A string without
// toISOString's shape gives 0.
func TimeValue(s string) int64 {
	m := isoShape.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	n := func(i int) int64 { v, _ := strconv.ParseInt(m[i], 10, 64); return v }
	return daysFromCivil(n(1), n(2), n(3))*86_400_000 + ((n(4)*60+n(5))*60+n(6))*1000 + n(7)
}

func daysInMonth(year, month int64) int64 {
	switch month {
	case 2:
		if (year%4 == 0 && year%100 != 0) || year%400 == 0 {
			return 29
		}
		return 28
	case 4, 6, 9, 11:
		return 30
	}
	return 31
}

// daysFromCivil is the proleptic Gregorian day number relative to 1970-01-01.
func daysFromCivil(y, m, d int64) int64 {
	if m <= 2 {
		y--
	}
	era := y / 400
	if y < 0 && y%400 != 0 {
		era--
	}
	yoe := y - era*400
	mp := (m + 9) % 12
	doy := (153*mp+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}
