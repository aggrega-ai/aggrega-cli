package probecore

import cerrors "example.test/raised/pkg/errors"

// Declared in a shared core that builds for host and guest alike. Its only
// raise site is the guest entry point beside it.
func ErrGuestRaised() error { return errGuestRaised }

var errGuestRaised = cerrors.Register("probecore/guest-raised", cerrors.Meta{
	ShortCode: "E9109",
	Message:   "raised only from the guest entry point",
})
