package thing

import (
	stderrors "errors"
	"fmt"
)

type dispatch struct{ boom error }

// The accessor is invoked here, inside a package-level composite literal, and
// the value is returned from the function below through the struct field.
var table = dispatch{boom: ErrStructLiteral()}

func raise() error { return fmt.Errorf("%w: dispatched", table.boom) }

// A match, not a raise.
func matched(err error) bool { return stderrors.Is(err, ErrMatchedOnly()) }

// The in-package raise: the binding is named, and the accessor is not written
// here because it yields the wrong type.
func raiseThroughBinding() error { return fmt.Errorf("%w: through the binding", errHandleRaised) }

// A match written in the in-package spelling. A bare binding as a matcher
// argument is the shape the mention reading makes reachable, so it is the shape
// the matcher exclusion has to disqualify.
func matchedThroughBinding(err error) bool { return stderrors.Is(err, errHandleMatchedOnly) }

var _, _, _, _ = raise, matched, raiseThroughBinding, matchedThroughBinding
