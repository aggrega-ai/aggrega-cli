package other

import (
	cerrors "example.test/fixture/pkg/errors"
	"example.test/fixture/pkg/thing"
)

// A re-export whose origin a reader can reach. Both names are importable, so the
// rendering is expected to say the two match each other.
const ErrPlain = thing.ErrPlain

const ErrOwn cerrors.Code = "other/own"

var _ = cerrors.Register(ErrOwn, cerrors.Meta{
	Message: "declared here",
})
