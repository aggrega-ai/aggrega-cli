//go:build !unix

// Host code excluded from some targets and restricted to none of them: a build
// without the tag satisfies the constraint, so the file enters a host binary and
// the condition it declares is matchable. It is the case a predicate that merely
// evaluated the constraint once, with every tag held true, would drop — `!unix`
// is false exactly then — so it stands guard over that reading.
package portable

import cerrors "example.test/guestsplit/pkg/errors"

func ErrNonUnixFallback() error { return errNonUnixFallback }

var errNonUnixFallback = cerrors.Register("portable/non-unix-fallback", cerrors.Meta{
	Message: "raised where the platform has no unix primitive",
})
