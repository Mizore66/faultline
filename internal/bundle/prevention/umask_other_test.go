//go:build !unix

package prevention

import "os"

func currentUmask() os.FileMode { return 0 }
