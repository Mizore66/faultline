package jsdate

import "testing"

func TestIsCanonicalISO(t *testing.T) {
	for s, want := range map[string]bool{
		"2026-07-16T12:00:00.000Z": true, "2024-02-29T00:00:00.000Z": true, "0000-02-29T00:00:00.000Z": true,
		"2026-02-29T00:00:00.000Z": false, "2026-02-30T00:00:00.000Z": false, "2026-01-01T24:00:00.000Z": false,
		"2026-01-31T23:59:60.000Z": false, "2026-13-01T00:00:00.000Z": false, "2026-04-31T00:00:00.000Z": false,
		"2100-02-29T00:00:00.000Z": false, "2000-02-29T00:00:00.000Z": true,
	} {
		if IsCanonicalISO(s) != want {
			t.Errorf("%s", s)
		}
	}
}
