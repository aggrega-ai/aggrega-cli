package other

import (
	cerrors "example.test/fixture/pkg/errors"
	"example.test/fixture/pkg/thing"
)

// A re-export whose origin a reader can reach. Both names are importable, so the
// rendering is expected to say the two match each other.
func ErrPlain() error { return thing.ErrPlain() }

func ErrOwn() error { return errOwn }

var errOwn = cerrors.Register("other/own", cerrors.Meta{
	Message: "declared here",
})
