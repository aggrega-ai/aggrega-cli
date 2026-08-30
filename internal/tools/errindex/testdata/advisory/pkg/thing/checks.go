package thing

import "example.test/advisory/internal/compiler/constraint"

var advisedCheck = constraint.Check[constraint.View, constraint.Subject]{
	Sentinel: ErrAdvised(),
	Severity: constraint.SeverityWarning,
	Run:      func(constraint.View, constraint.Subject) []constraint.Objection { return nil },
}

// A check that halts. It has to be here: every assertion about the advisory set
// reads the same over a reader that marked every check, and only a fatal
// declaration in the same fixture tells the two apart.
var haltingCheck = constraint.Check[constraint.View, constraint.Subject]{
	Sentinel: ErrHalting(),
	Severity: constraint.SeverityFatal,
	Run:      func(constraint.View, constraint.Subject) []constraint.Objection { return nil },
}

// A check written as an element of a larger literal rather than as a
// declaration of its own. Reading only the package-level shape would miss it,
// and the miss would be invisible: a table short by one row reads exactly like a
// module with one fewer advisory.
var registeredChecks = []constraint.Check[constraint.View, constraint.Subject]{
	{
		Sentinel: ErrNestedAdvised(),
		Severity: constraint.SeverityWarning,
		Run:      func(constraint.View, constraint.Subject) []constraint.Objection { return nil },
	},
}
