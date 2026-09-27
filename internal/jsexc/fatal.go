package jsexc

import (
	"os"
	"strconv"
)

// FatalInvalidSize reproduces V8's V8_Fatal for an allocation beyond its
// size limits ("Fatal JavaScript invalid size error <n>"): the report
// header on stderr, then the process dies by the breakpoint trap V8 uses
// (exit status 133 on Unix). Node's native stack trace is not reproduced.
func FatalInvalidSize(n int) {
	os.Stderr.WriteString("\n\n#\n# Fatal error in , line 0\n# Fatal JavaScript invalid size error " +
		strconv.Itoa(n) + " (see crbug.com/1201626)\n#\n#\n#\n#FailureMessage Object: 0x0\n")
	trap()
}

// FatalOOM reproduces Node's fatal out-of-memory report for a V8 allocation
// that cannot succeed (such as "invalid table size"), then dies by SIGABRT
// (exit status 134 on Unix). Node's GC trace and native stack trace are not
// reproduced.
func FatalOOM(what string) {
	os.Stderr.WriteString("\n<--- Last few GCs --->\n\n\n<--- JS stacktrace --->\n\nFATAL ERROR: " + what +
		" Allocation failed - JavaScript heap out of memory\n----- Native stack trace -----\n\n")
	abort()
}
