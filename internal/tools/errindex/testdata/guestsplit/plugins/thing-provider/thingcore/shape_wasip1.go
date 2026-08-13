// Restricted to the guest target by its file name alone, carrying no //go:build
// line at all. It exercises the second axis a build constraint can be written
// on: a scan reading only the constraint comment would index this declaration
// while the toolchain compiles it into no host binary.
package thingcore

import cerrors "example.test/guestsplit/pkg/errors"

func ErrSuffixConstrained() error { return errSuffixConstrained }

var errSuffixConstrained = cerrors.Register("thing-provider-thingcore/suffix-constrained", cerrors.Meta{
	Message: "raised only where the file name restricts the build",
})
