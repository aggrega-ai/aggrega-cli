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

var _, _ = raise, matched
