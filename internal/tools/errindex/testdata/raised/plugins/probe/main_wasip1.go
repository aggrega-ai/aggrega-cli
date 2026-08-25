package main

import (
	"fmt"

	"example.test/raised/plugins/probe/probecore"
)

// A guest entry point. It declares nothing a host caller can match, so the scan
// reads no declaration from it — but it is production code that runs, so the
// raise below is a raise.
func run() error { return fmt.Errorf("%w: from the guest", probecore.ErrGuestRaised()) }

var _ = run
