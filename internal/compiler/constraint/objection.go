// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

import (
	"fmt"
	"strings"
)

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

// ObjectionMessage renders what one check raised as the text a diagnostic
// carries: the sentinel's registered wording, then the values the check
// supplied.
//
// It lives here, beside the objection, because it reads nothing else. A
// boundary converting an objection into its own diagnostic shape decides which
// of its fields the provenance lands on, and that decision differs per boundary
// — but the wording does not, and a second boundary composing it again would be
// free to compose it differently. One renderer is what makes "every rule
// reporting this way reports identically" a property rather than an intention,
// and the package that owns the objection is the only home every boundary can
// reach.
//
// The message opens with the sentinel's own registered text, so a rule's
// wording lives with the rule's identity and a second copy cannot drift from
// it. The values the check supplied follow it, because the registered text is
// one static string per rule and cannot carry them — so a message that were
// only that text would drop the very thing an author has to act on.
//
// The values are rendered rather than described, because this reads every rule
// and knows none of them: a check's detail is an ordered list of strings whose
// meaning is the rule's, so any attempt here to caption them would be one
// renderer holding a copy of every rule's grammar. Each is quoted, which keeps
// a value carrying a space or a comma readable as one value and makes a value
// the check supplied empty visible instead of vanishing into the separator.
//
// A check that supplied no detail gets the registered wording alone, so a rule
// whose identity says the whole of it reads as a sentence rather than as one
// with an empty parenthesis after it.
func ObjectionMessage(o Objection) string {
	registered := o.Sentinel().Error()
	detail := o.Detail()
	if len(detail) == 0 {
		return registered
	}
	quoted := make([]string, 0, len(detail))
	for _, d := range detail {
		quoted = append(quoted, fmt.Sprintf("%q", d))
	}
	return registered + " (" + strings.Join(quoted, ", ") + ")"
}
