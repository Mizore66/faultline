// Package jsexc carries JavaScript exceptions through Go panics, so ported
// code keeps TS try/catch control flow. Packages below internal/bundle (such
// as internal/schema, whose ZodError message getter can throw) use it too.
package jsexc

// Thrown carries a JS exception through a Go panic.
type Thrown struct{ Err error }

// Throw is a JS `throw err`.
func Throw(err error) { panic(Thrown{err}) }

// Must returns v, or throws err.
func Must[T any](v T, err error) T {
	if err != nil {
		Throw(err)
	}
	return v
}

// Try runs fn like a JS try block and returns the caught exception.
func Try(fn func()) (err error) {
	defer Catch(func(e error) { err = e })
	fn()
	return nil
}

// Catch recovers a Thrown panic; use as `defer jsexc.Catch(handler)`.
func Catch(handler func(error)) {
	if r := recover(); r != nil {
		t, ok := r.(Thrown)
		if !ok {
			panic(r)
		}
		handler(t.Err)
	}
}
