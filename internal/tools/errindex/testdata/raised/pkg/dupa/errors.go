package dupa

import (
	"fmt"

	cerrors "example.test/raised/pkg/errors"
)

// One of two same-named conditions in different packages. THIS one is raised.
func ErrSameName() error { return errSameName }

var errSameName = cerrors.Register("dupa/same-name", cerrors.Meta{
	ShortCode: "E9107",
	Message:   "the raised one of the same-named pair",
})

func raise() error { return fmt.Errorf("%w", ErrSameName()) }

var _ = raise
