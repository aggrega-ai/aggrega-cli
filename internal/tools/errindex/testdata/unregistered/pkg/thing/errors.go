package thing

import "errors"

// Published with no registration behind it. A caller matching this handle would
// match whatever the returned name happens to hold rather than a declared
// condition, so the page has nothing truthful to render for it.
func ErrOrphan() error { return errOrphan }

var errOrphan = errors.New("thing: a condition this package never registered")
