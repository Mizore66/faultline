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
