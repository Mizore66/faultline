//go:build linux && cgo

package boot

/*
#include <signal.h>
#include <stdlib.h>

// The dispositions fl inherited, recorded by a C constructor, which runs
// before the Go runtime replaces them with its own handlers (as runc's
// nsenter does). The image that clears an inherited signal mask passes its
// record on in FAULTLINE_BOOT_IGNORED, since by then the runtime has
// replaced them in its own process.
static unsigned long long faultline_ignored;

__attribute__((constructor)) static void faultline_record_ignored(void) {
	const char *passed = getenv("FAULTLINE_BOOT_IGNORED");
	if (passed != NULL) {
		faultline_ignored = strtoull(passed, NULL, 16);
		return;
	}
	for (int s = 1; s <= 64; s++) {
		struct sigaction sa;
		if (sigaction(s, NULL, &sa) == 0 && sa.sa_handler == SIG_IGN)
			faultline_ignored |= 1ULL << (s - 1);
	}
}

static unsigned long long faultline_inherited_ignored(void) { return faultline_ignored; }
*/
import "C"

// inheritedIgnored is the set of signals fl inherited as SIG_IGN (bit
// sig-1), and whether it is known.
func inheritedIgnored() (uint64, bool) { return uint64(C.faultline_inherited_ignored()), true }
