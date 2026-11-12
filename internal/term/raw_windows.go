package term

import "os"

// MakeRaw is not implemented on Windows: callers fall back to the console's
// line-input mode (a recorded, open gap against Node's raw console readline).
func MakeRaw(file *os.File) (restore func(), ok bool) { return nil, false }
