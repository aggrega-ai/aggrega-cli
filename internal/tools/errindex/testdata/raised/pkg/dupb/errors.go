package dupb

import cerrors "example.test/raised/pkg/errors"

// The other of the two same-named conditions. Nothing raises this one. A
// detector keyed on the bare name would let its twin vouch for it.
func ErrSameName() error { return errSameName }

var errSameName = cerrors.Register("dupb/same-name", cerrors.Meta{
	ShortCode: "E9108",
	Message:   "the unraised one of the same-named pair",
})
