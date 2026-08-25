package thing

import cerrors "example.test/raised/pkg/errors"

// Raised only through a package-level struct literal that a shared dispatcher
// returns. This is the shape a detector reading an occurrence's syntactic
// position cannot see, and it is live.
func ErrStructLiteral() error { return errStructLiteral }

var errStructLiteral = cerrors.Register("thing/struct-literal", cerrors.Meta{
	ShortCode: "E9101",
	Message:   "raised through a dispatcher table",
})

// Named only as the argument of a match. Matching asks whether something else
// already raised the condition, so this one is raised by nothing.
func ErrMatchedOnly() error { return errMatchedOnly }

var errMatchedOnly = cerrors.Register("thing/matched-only", cerrors.Meta{
	ShortCode: "E9102",
	Message:   "only ever matched",
})

// Named nowhere at all, and carrying no note.
func ErrUntouched() error { return errUntouched }

var errUntouched = cerrors.Register("thing/untouched", cerrors.Meta{
	ShortCode: "E9103",
	Message:   "reached by nothing",
})

// Raised by nothing, but declared ahead of the work that will raise it.
func ErrAhead() error { return errAhead }

var errAhead = cerrors.Register("thing/ahead", cerrors.Meta{
	ShortCode:     "E9104",
	Message:       "declared ahead",
	DeclaredAhead: "It will report the fixture condition once the fixture raises it.",
})

// The note is written as a concatenation, which is how one too long for a line
// is written. It has to read the same as a single literal.
func ErrFolded() error { return errFolded }

var errFolded = cerrors.Register("thing/folded", cerrors.Meta{
	ShortCode: "E9105",
	Message:   "declared ahead across two literals",
	DeclaredAhead: "It will report the folded fixture condition " +
		"once the fixture raises it.",
})

// Declared here and raised nowhere in this package; the only raise is through
// the re-export in another package.
func ErrRaisedViaReExport() error { return errRaisedViaReExport }

var errRaisedViaReExport = cerrors.Register("thing/via-re-export", cerrors.Meta{
	ShortCode: "E9106",
	Message:   "raised only through its second name",
})
