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
// to produce a populated Objection is [Subject.Raise] or [Subject.RaiseWith],
// and a Subject comes only from this package. The zero value is constructible,
// as it is for any Go struct, and carries no sentinel and no provenance —
// [Objection.Sentinel] returns nil for it, which is how a reader tells it apart
// from a raised objection.
type Objection struct {
	sentinel error
	file     string
	locator  string
	detail   []DetailValue
}

// DetailValue is one value a check supplied, and the caption naming what that
// value is.
//
// A caption exists because a rule's registered text is one static string and
// names one thing. The value that thing refers to needs no caption: the
// sentence has already said what it is, and a caption would repeat the
// sentence. Every further value the rule supplies is something the sentence does
// not name, and a reader handed two bare quoted strings has no way to tell which
// is which — an artifact's output name and a configuration pillar reference are
// both filenames or both dotted words, and the rule is the only thing that knows
// which is which.
//
// So the convention a rule authors against is: leave bare the value the
// registered text already names, and caption every other one. Build the two
// forms with [Uncaptioned] and [Captioned] and raise them with
// [Subject.RaiseWith].
//
// A caption is the rule's own word for what it holds, so it is written in the
// vocabulary the author of the offending document reads — the key they typed,
// the block they wrote it in — and never a name for something only the compiler
// can see.
//
// Its fields are unexported and there is no literal form, so a value reaches a
// message only through one of the two constructors. That is the same seal the
// objection itself carries, for the same reason: the shape a diagnostic is
// rendered from is this package's to decide.
type DetailValue struct {
	caption string
	value   string
}

// Uncaptioned is the detail value a rule's registered text already names, which
// is why it carries no caption.
func Uncaptioned(value string) DetailValue {
	return DetailValue{value: value}
}

// Captioned is a detail value the rule's registered text does not name, carrying
// the caption that says what it is.
//
// caption is prose and not a key: it is read as part of a sentence, so it is
// spelled as the words that precede the value rather than as an identifier with
// a separator in it.
func Captioned(caption, value string) DetailValue {
	return DetailValue{caption: caption, value: value}
}

// render spells one detail value as a message reads it: the quoted value, with
// the caption ahead of it where the rule supplied one.
//
// The value is quoted whether or not it is captioned, which keeps a value
// carrying a space or a comma readable as one value and makes a value the check
// supplied empty visible instead of vanishing into the separator.
func (d DetailValue) render() string {
	quoted := fmt.Sprintf("%q", d.value)
	if d.caption == "" {
		return quoted
	}
	return d.caption + " " + quoted
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
//
// It carries the values and not their captions. A caption says how a value reads
// inside a sentence, which is a question only a rendered message asks, and a
// boundary reading an objection structurally asks the other one: it reads the
// sentinel to know which rule spoke and the provenance to know where, and wants
// the value itself rather than its presentation. A boundary that needs the
// captions reads [Objection.RenderedDetail], which spells them the way a message
// does; the two accessors are the two questions and neither substitutes for the
// other.
func (o Objection) Detail() []string {
	if len(o.detail) == 0 {
		return nil
	}
	out := make([]string, 0, len(o.detail))
	for _, d := range o.detail {
		out = append(out, d.value)
	}
	return out
}

// RenderedDetail returns the check's detail values spelled as a message reads
// them: each value quoted, with the rule's caption ahead of it where the rule
// supplied one. The result is a copy, so a caller cannot reach back into the
// objection, and it is nil when the check supplied no detail.
//
// It exists for a boundary that composes its own diagnostic AROUND the rule's
// registered wording rather than taking the whole rendered message. Such a
// boundary contributes subject of its own — the document a finding is attributed
// to, the locator it is positioned at — and has to place the rule's values among
// them, which needs the values in the form a message reads. [Objection.Detail]
// deliberately does not give that form, and taking them from [ObjectionMessage]
// would not give them at all: its result OPENS with the registered sentence, so
// a composer that leads with that sentence itself would state it twice.
//
// [ObjectionMessage] renders from this, so a detail list has one spelling
// however many boundaries read one.
func (o Objection) RenderedDetail() []string {
	if len(o.detail) == 0 {
		return nil
	}
	out := make([]string, 0, len(o.detail))
	for _, d := range o.detail {
		out = append(out, d.render())
	}
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
// and knows none of them: a check's detail is an ordered list of values whose
// meaning is the rule's, so any attempt here to caption them would be one
// renderer holding a copy of every rule's grammar. Each is quoted, which keeps
// a value carrying a space or a comma readable as one value and makes a value
// the check supplied empty visible instead of vanishing into the separator.
//
// That refusal stands, and it is what fixes where a caption comes from rather
// than whether there is one. A caption travels with the value the rule supplied,
// so the grammar stays with the rule and what is held here is only the
// convention it is spelled by: a captioned value reads as its caption then the
// quoted value, an uncaptioned one as the quoted value alone, and the two mix
// freely in one message because a rule ordinarily has exactly one value its
// registered text already names. See [DetailValue] for which of a rule's values
// carries a caption and which does not. A rule that captions nothing renders
// from the quoted values alone, so the convention costs such a rule nothing.
//
// A check that supplied no detail gets the registered wording alone, so a rule
// whose identity says the whole of it reads as a sentence rather than as one
// with an empty parenthesis after it.
func ObjectionMessage(o Objection) string {
	registered := o.Sentinel().Error()
	rendered := o.RenderedDetail()
	if len(rendered) == 0 {
		return registered
	}
	return registered + " (" + strings.Join(rendered, ", ") + ")"
}
