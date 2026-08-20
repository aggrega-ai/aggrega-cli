package hidden

import cerrors "example.test/fixture/pkg/errors"

func ErrHidden() error { return errHidden }

var errHidden = cerrors.Register("hidden/hidden", cerrors.Meta{
	ShortCode: "E9005",
	Message:   "raised from a package no reader can import",
})
