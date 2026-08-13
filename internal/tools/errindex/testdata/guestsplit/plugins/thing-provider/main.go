//go:build wasip1

// A guest's entry point: constrained to the WebAssembly guest target, so it is
// compiled into no host binary and publishes nothing a caller can match. Its
// registration is real and its accessor is exported, so what keeps it out of the
// index is the build constraint alone — which is what this fixture holds the
// scan to.
package main

import cerrors "example.test/guestsplit/pkg/errors"

func ErrGuestEntryOnly() error { return errGuestEntryOnly }

var errGuestEntryOnly = cerrors.Register("thing-provider/guest-entry-only", cerrors.Meta{
	Message: "raised from a guest entry point",
})

func main() {}
