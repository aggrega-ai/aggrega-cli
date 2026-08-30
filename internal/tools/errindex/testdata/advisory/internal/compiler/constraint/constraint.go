package constraint

import cerrors "example.test/advisory/pkg/errors"

// The fixture's stand-in for the package that declares a check and the two
// severities one chooses between. Only its module-relative directory matters to
// the scan, which recognises a check by the package its type is qualified with.

type Severity string

const (
	SeverityFatal   Severity = "fatal"
	SeverityWarning Severity = "warning"
)

type Objection struct{}

type Check[V any, S any] struct {
	Sentinel error
	Severity Severity
	Run      func(V, S) []Objection
}

type View struct{}

type Subject struct{}

// A condition the constraint package declares itself, so the unqualified
// spelling of both the type and the severity is exercised. A file that IS the
// constraint package writes `Check[…]` and `SeverityWarning` bare, and a reader
// that only understood the qualified spelling would miss every such declaration
// while reporting nothing.
func ErrInPackage() error { return errInPackage }

var errInPackage = cerrors.Register("constraint/in-package", cerrors.Meta{
	ShortCode: "E9811",
	Message:   "declared and advised from within the constraint package",
})

var inPackageAdvisory = Check[View, Subject]{
	Sentinel: ErrInPackage(),
	Severity: SeverityWarning,
	Run:      func(View, Subject) []Objection { return nil },
}
