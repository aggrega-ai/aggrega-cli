package other

import (
	"fmt"

	"example.test/raised/pkg/thing"
)

// A re-export. Its own body invokes the origin's accessor, and that invocation
// is NOT a raise: it publishes the identity under a second name and reaches
// nothing.
func ErrRaisedViaReExport() error { return thing.ErrRaisedViaReExport() }

// The real raise, through the second name. Credit belongs to the origin.
func raise() error { return fmt.Errorf("%w: here", ErrRaisedViaReExport()) }

// A re-export of a condition raised in its own package, so both names should
// read as raised without this file raising anything.
func ErrStructLiteral() error { return thing.ErrStructLiteral() }

var _ = raise
