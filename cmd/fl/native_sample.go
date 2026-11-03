package main

import (
	"os"

	"github.com/Mizore66/faultline/internal/nativefixture"
)

// The marker selects the fixed private protocol; it is not authentication.
func runNativeSample() (code int, handled bool) {
	return nativefixture.DispatchChild(os.Args[1:], os.Getenv(nativefixture.ChildEnvName), os.Stdout, os.Stderr)
}
