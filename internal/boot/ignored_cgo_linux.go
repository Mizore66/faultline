//go:build linux && cgo

package boot

/*
#include <signal.h>
#include <stdlib.h>
#include <string.h>

// The dispositions fl inherited, recorded by a C constructor, which runs
// before the Go runtime replaces them with its own handlers (as runc's
// nsenter does). The image that clears an inherited signal mask passes its
// record on in FAULTLINE_BOOT_IGNORED (the set's words in hex, separated by
// commas), since by then the runtime has replaced them in its own process.
// The set is the kernel's sigset_t: signal s is bit s-1 of an array of
// unsigned long, for every signal below _NSIG (65; 129 on MIPS).
#define FAULTLINE_BITS (8 * sizeof(unsigned long))
#define FAULTLINE_WORDS ((_NSIG - 1 + FAULTLINE_BITS - 1) / FAULTLINE_BITS)
static unsigned long faultline_ignored[FAULTLINE_WORDS];

__attribute__((constructor)) static void faultline_record_ignored(void) {
	const char *passed = getenv("FAULTLINE_BOOT_IGNORED");
	if (passed != NULL) {
		for (size_t w = 0; w < FAULTLINE_WORDS && *passed != '\0'; w++) {
			char *end;
			faultline_ignored[w] = strtoul(passed, &end, 16);
			passed = *end == ',' ? end + 1 : end;
		}
		return;
	}
	for (int s = 1; s < _NSIG; s++) {
		struct sigaction sa;
		if (sigaction(s, NULL, &sa) == 0 && sa.sa_handler == SIG_IGN)
			faultline_ignored[(s - 1) / FAULTLINE_BITS] |= 1UL << ((s - 1) % FAULTLINE_BITS);
	}
}

static size_t faultline_ignored_words(void) { return FAULTLINE_WORDS; }
static unsigned long faultline_ignored_word(size_t w) { return faultline_ignored[w]; }
*/
import "C"

import "github.com/Mizore66/faultline/internal/sigexit"

// inheritedIgnored is the set of signals fl inherited as SIG_IGN, and
// whether it is known.
func inheritedIgnored() (sigexit.Set, bool) {
	var s sigexit.Set
	for w := 0; w < len(s) && w < int(C.faultline_ignored_words()); w++ {
		s[w] = uintptr(C.faultline_ignored_word(C.size_t(w)))
	}
	return s, true
}
