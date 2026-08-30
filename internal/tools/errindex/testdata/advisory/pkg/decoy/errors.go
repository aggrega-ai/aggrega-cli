package decoy

import cerrors "example.test/advisory/pkg/errors"

// An unrelated generic type that happens to be called Check, with fields that
// happen to be called Sentinel and Severity, and a value that happens to be
// called SeverityWarning. Nothing here imports the constraint package, so a
// reader matching the bare type name would mark this condition advisory and the
// published table would carry a row no declaration supports.

type Severity string

const SeverityWarning Severity = "warning"

type Check[V any, S any] struct {
	Sentinel error
	Severity Severity
}

func ErrDecoy() error { return errDecoy }

var errDecoy = cerrors.Register("decoy/decoy", cerrors.Meta{
	ShortCode: "E9807",
	Message:   "named by something that is not a check",
})

var decoyCheck = Check[string, string]{
	Sentinel: ErrDecoy(),
	Severity: SeverityWarning,
}
