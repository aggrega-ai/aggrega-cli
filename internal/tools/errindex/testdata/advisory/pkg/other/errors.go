package other

import (
	"example.test/advisory/internal/compiler/constraint"
	"example.test/advisory/pkg/thing"
)

// A re-export: it declares no condition of its own and returns the value the
// origin publishes, so a check naming this name declares the severity of the one
// value both names reach.
func ErrReexported() error { return thing.ErrOriginOnly() }

var reexportedCheck = constraint.Check[constraint.View, constraint.Subject]{
	Sentinel: ErrReexported(),
	Severity: constraint.SeverityWarning,
	Run:      func(constraint.View, constraint.Subject) []constraint.Objection { return nil },
}
