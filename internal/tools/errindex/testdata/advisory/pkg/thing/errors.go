package thing

import cerrors "example.test/advisory/pkg/errors"

func ErrAdvised() error { return errAdvised }

var errAdvised = cerrors.Register("thing/advised", cerrors.Meta{
	ShortCode: "E9802",
	Message:   "reported without stopping the build",
})

func ErrHalting() error { return errHalting }

var errHalting = cerrors.Register("thing/halting", cerrors.Meta{
	ShortCode: "E9803",
	Message:   "stops the build",
})

func ErrNestedAdvised() error { return errNestedAdvised }

var errNestedAdvised = cerrors.Register("thing/nested-advised", cerrors.Meta{
	ShortCode: "E9804",
	Message:   "advised from a check written inside a larger literal",
})

// The origin a re-export in another package forwards to. Nothing here declares
// its severity; the check that does names the re-exported name instead, so this
// identity is advisory only if the credit is propagated through the forward.
func ErrOriginOnly() error { return errOriginOnly }

var errOriginOnly = cerrors.Register("thing/origin-only", cerrors.Meta{
	ShortCode: "E9805",
	Message:   "advised through the name a second package re-exports",
})

// A condition nothing raises and no check names, so it is neither advisory nor
// admitted. Without it every assertion below would read the same over a fixture
// in which the reader marked everything.
func ErrUntouched() error { return errUntouched }

var errUntouched = cerrors.Register("thing/untouched", cerrors.Meta{
	ShortCode: "E9806",
	Message:   "named by no check at all",
})
