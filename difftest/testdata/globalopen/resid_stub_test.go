//go:build linux && globalopen_hooks && !globalopen_residual

package main

import "os"

// Without the residual tests compiled in (tag globalopen_residual), the
// "resid" fixture mode is never requested; this stub only satisfies TestMain.
func childResid(target, variant string) { os.Exit(99) }
