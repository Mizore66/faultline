package jsexc

import (
	"os"
	"strconv"
)

// FatalInvalidSize reproduces V8's V8_Fatal for an allocation beyond its
// size limits ("Fatal JavaScript invalid size error <n>"): the report
// header on stderr, then the process dies by the breakpoint trap V8 uses
// (exit status 133 on Unix). Node's native stack trace is not reproduced.
// JSON.parse arrays report "(see crbug.com/1201626)" after the length.
func FatalInvalidSize(n int) {
	fatalInvalidSize(strconv.Itoa(n) + " (see crbug.com/1201626)")
}

// FatalInvalidFixedArraySize is FatalInvalidSize for the other FixedArray
// allocations (an object's elements store, String.prototype.split), which
// report the length alone.
func FatalInvalidFixedArraySize(n int) { fatalInvalidSize(strconv.Itoa(n)) }

func fatalInvalidSize(what string) {
	os.Stderr.WriteString("\n\n#\n# Fatal error in , line 0\n# Fatal JavaScript invalid size error " +
		what + "\n#\n#\n#\n#FailureMessage Object: 0x0\n")
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

// FatalCheck reproduces a failed CHECK in Node's C++ (node::Assert): the
// assertion header with the process title (argv[0] until a script sets
// process.title) and id, the function and source location
// as a Linux build of Node 22.22.2 prints them, and the failed expression,
// then death by SIGABRT (exit status 134 on Unix). Node's native and
// JavaScript stack traces are not reproduced.
func FatalCheck(where, expr string) {
	os.Stderr.WriteString("\n  #  " + os.Args[0] + "[" + strconv.Itoa(os.Getpid()) + "]: " + where + "\n  #  Assertion failed: " + expr +
		"\n\n----- Native stack trace -----\n\n")
	abort()
}
