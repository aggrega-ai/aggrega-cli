// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

// Objection is what a check hands back when an input violates the rule the
// check enforces: the sentinel naming the rule, the provenance of the subject
// the objection is about, and the check's detail.
//
// It is deliberately not a diagnostic. The framework converts an objection
// into whatever shape the caller reading it needs, so a check stays ignorant
// of which boundary its output crosses and no conversion step can drop the
// provenance the framework supplied.
//
// Every field is unexported and there is no exported constructor: the only way
// to produce a populated Objection is [Subject.Raise], and a Subject comes
// only from this package. The zero value is constructible, as it is for any Go
// struct, and carries no sentinel and no provenance — [Objection.Sentinel]
// returns nil for it, which is how a reader tells it apart from a raised
// objection.
type Objection struct {
	sentinel error
	file     string
	locator  string
	detail   []string
}

// Sentinel returns the sentinel naming the rule the check enforces. It is nil
// for the zero Objection, which is not a raised objection.
func (o Objection) Sentinel() error { return o.sentinel }

// SourceFile returns the workspace-relative file the subject was authored in,
// or the empty string when the subject has no single-file origin.
func (o Objection) SourceFile() string { return o.file }

// Locator returns the dotted locator naming the authored element the subject
// stands for, or the empty string when no authored locator is derivable. It
// never carries a line or a column; see the package documentation.
func (o Objection) Locator() string { return o.locator }

// Detail returns the check's detail values in the order the check supplied
// them. The result is a copy, so a caller cannot reach back into the
// objection, and it is nil when the check supplied no detail.
func (o Objection) Detail() []string {
	if len(o.detail) == 0 {
		return nil
	}
	out := make([]string, len(o.detail))
	copy(out, o.detail)
	return out
}
