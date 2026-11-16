//go:build linux || darwin || windows

package proofview

// NATIVE-DIAGNOSTIC producers for the unix remainder core, one child per actual
// unixRemainderError.Error() producer position (8 total). Each child drives the
// ACTUAL core through the portable fakes (urfRaw/udrRaw/urfWritev/urfObs and the
// urf* call helpers from socket_remainder_unix_test.go and
// socket_remainder_domain_unix_test.go, which the parent retags later) and
// builds ONE observed string from the error the core returned. `want` is an
// independent literal compared BEFORE the single record is logged; the record
// carries only the observed string. Only the unknown child calls Error()
// directly on a constructed enum value (there is no core path producing it).
// Success-only and lifecycle scenarios stay in the ordinary unit tests.

import (
	"encoding/json"
	"errors"
	"syscall"
	"testing"
)

// urNoCbRaw is a RawConn whose Write returns nil without ever calling the callback.
type urNoCbRaw struct{}

func (urNoCbRaw) Control(func(uintptr)) error    { return errors.New("unused") }
func (urNoCbRaw) Read(func(uintptr) bool) error  { return errors.New("unused") }
func (urNoCbRaw) Write(func(uintptr) bool) error { return nil }

var _ syscall.RawConn = urNoCbRaw{}

// urObserved picks the error the core reported, from the API error or result.Err.
func urObserved(t *testing.T, o urfOut) string {
	t.Helper()
	switch {
	case o.err != nil:
		return o.err.Error()
	case o.res.Err != nil:
		return o.res.Err.Error()
	}
	t.Fatalf("core reported no error: %+v", o.res)
	return ""
}

func TestUnixRemainderNativeDiagnostics(t *testing.T) {
	cases := []struct {
		name string
		want string
		run  func(t *testing.T) string
	}{
		{"busy", "unix remainder: busy", func(t *testing.T) string {
			// Parked partial Submit leaves the core Armed; a second Submit must be refused.
			r, o, w := newURFRaw(t), newURFObs(t), &urfWritev{script: []urfStep{{n: 3}}}
			c := newUnixRemainderWithDeps(unixRemainderDeps{Raw: r, Writev: w.fn, Observe: o.observe,
				Close: func() error { r.doEvict(); <-r.rawDone; return nil }})
			if f := urfSubmit(t, c, urfParts()); f.err != nil || f.res.N != 3 {
				t.Fatalf("first Submit: %+v err=%v", f.res, f.err)
			}
			urfRecv(t, r.waitEntered, "worker parked")
			got := urObserved(t, urfSubmit(t, c, urfParts()))
			if err := urfClose(t, c); err != nil { // evicts the parked worker
				t.Fatalf("Close: %v", err)
			}
			urfRecv(t, o.workerExit, "worker_exit after cleanup Close")
			return got
		}},
		{"not_ready", "unix remainder: token not ready", func(t *testing.T) string {
			r, o, w := newUDRRaw(t, udrIdle), newURFObs(t), &urfWritev{}
			c := udrComponent(r, w, o, nil)
			return urObserved(t, urfKick(t, c, unixRemainderReady{Conn: 1, Cycle: 1}, [][]byte{[]byte("x")}))
		}},
		{"too_many_vectors", "unix remainder: too many vectors", func(t *testing.T) string {
			r, o, w := newUDRRaw(t, udrIdle), newURFObs(t), &urfWritev{}
			c := udrComponent(r, w, o, nil)
			parts := make([][]byte, 9)
			for i := range parts {
				parts[i] = udrOne(1)
			}
			return urObserved(t, urfSubmit(t, c, parts))
		}},
		{"too_large", "unix remainder: input too large", func(t *testing.T) string {
			r, o, w := newUDRRaw(t, udrIdle), newURFObs(t), &urfWritev{}
			c := udrComponent(r, w, o, nil)
			return urObserved(t, urfSubmit(t, c, [][]byte{make([]byte, 64<<20+1)})) // real allocation
		}},
		{"closed", "unix remainder: closed", func(t *testing.T) string {
			r, o, w := newUDRRaw(t, udrIdle), newURFObs(t), &urfWritev{}
			c := udrComponent(r, w, o, nil)
			if err := urfClose(t, c); err != nil {
				t.Fatalf("Close: %v", err)
			}
			return urObserved(t, urfSubmit(t, c, urfParts()))
		}},
		{"invalid_native", "unix remainder: invalid native write result", func(t *testing.T) string {
			r, o := newURFRaw(t), newURFObs(t)
			r.releaseWait()
			w := &urfWritev{script: []urfStep{{n: -1}}}
			c := newURFComponent(r, w, o, nil)
			got := urObserved(t, urfSubmit(t, c, urfParts()))
			urfRecv(t, o.workerExit, "worker_exit")
			return got
		}},
		{"no_attempt", "unix remainder: raw write returned without an attempt", func(t *testing.T) string {
			o, w := newURFObs(t), &urfWritev{}
			c := newUnixRemainderWithDeps(unixRemainderDeps{Raw: urNoCbRaw{}, Writev: w.fn,
				Close: func() error { return nil }, Observe: o.observe})
			got := urObserved(t, urfSubmit(t, c, urfParts()))
			urfRecv(t, o.workerExit, "worker_exit")
			if w.calls.Load() != 0 {
				t.Fatalf("writev ran %d time(s) though the callback never ran", w.calls.Load())
			}
			return got
		}},
		{"unknown", "unix remainder: unknown error", func(t *testing.T) string {
			return unixRemainderError(0).Error() // 256 is not representable in uint8; 0 is the invalid value
		}},
	}
	if len(cases) != 8 {
		t.Fatalf("diagnostic positions=%d, want exactly 8", len(cases))
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			got := c.run(t)
			if got != c.want {
				t.Fatalf("observed %q does not match the independent expectation %q", got, c.want)
			}
			observed, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("NATIVE-DIAGNOSTIC %s", observed)
		})
	}
}
