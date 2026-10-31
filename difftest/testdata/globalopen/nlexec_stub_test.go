//go:build linux && globalopen_hooks && !nonleader_exec_staged

package main

import "os"

// Without the nonleader-exec tests compiled in (tag nonleader_exec_staged), the
// "nlexec" fixture mode is never requested; this stub only satisfies TestMain.
func childNlexec() { os.Exit(99) }
