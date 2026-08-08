package hidden

import cerrors "example.test/fixture/pkg/errors"

const ErrHidden cerrors.Code = "hidden/hidden"

var _ = cerrors.Register(ErrHidden, cerrors.Meta{
	Message: "raised from a package no reader can import",
})
