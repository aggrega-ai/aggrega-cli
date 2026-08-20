// The translation core beside the guest: it carries no build constraint, so it
// builds for the host and for the guest alike and the condition it declares is
// one a caller reaches through an ordinary import. It sits under the plugin tree
// and must still be indexed.
package thingcore

import cerrors "example.test/guestsplit/pkg/errors"

func ErrCoreShared() error { return errCoreShared }

var errCoreShared = cerrors.Register("thing-provider-thingcore/core-shared", cerrors.Meta{
	ShortCode: "E9103",
	Message:   "raised from a core the host and the guest both build",
})
