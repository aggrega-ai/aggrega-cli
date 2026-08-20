package thing

import cerrors "example.test/noshortcode/pkg/errors"

// Registered with a message and no short code. The real registration refuses
// this at package initialisation, so a tree in this state cannot run — but the
// scan reads syntax rather than a compiled program, so it meets the state anyway
// and must refuse it by name. Serving it would print a listing whose short-code
// column is blank for this row, which reads as a condition that has no short
// code rather than as a declaration that forgot one.
func ErrNoShortCode() error { return errNoShortCode }

var errNoShortCode = cerrors.Register("thing/no-short-code", cerrors.Meta{
	Message: "a condition whose declaration omits its short code",
})
