//go:build linux && globalopen_hooks && !log_controls

package main

import "os"

// Without the log/flags controls compiled in (tag log_controls), the
// "logfail" and "flags" fixture modes are never requested; these stubs only
// satisfy TestMain.
func childLogfail()            { os.Exit(99) }
func childFlags(target string) { os.Exit(99) }
