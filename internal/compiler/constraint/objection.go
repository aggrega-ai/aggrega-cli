// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

import (
	"fmt"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// Objection is what a check hands back when an input violates the rule the
// check enforces: the sentinel naming the rule, the provenance of the subject
// the objection is about, the check's detail, and — where the rule has one —
// the advice that applies to this occurrence.
//
// It is deliberately not a diagnostic. The framework converts an objection
// into whatever shape the caller reading it needs, so a check stays ignorant
// of which boundary its output crosses and no conversion step can drop the
// provenance the framework supplied.
//
// Every field is unexported and there is no exported constructor: the only way
// to produce a populated Objection is [Subject.Raise], [Subject.RaiseWith],
// [Subject.RaiseWithAdvice] or [Subject.RaiseAt], and a Subject comes only from
// this package. The zero value is constructible, as it is for any Go struct, and
// carries no sentinel and no provenance — [Objection.Sentinel] returns nil for
// it, which is how a reader tells it apart from a raised objection.
type Objection struct {
	sentinel error
	file     string
	locator  string
	detail   []DetailValue

	// document is which authoring document the subject was written in, empty
	// where the traversal that supplied the subject records none. See
	// [Objection.AuthoringDocument].
	document string

	// advice is what this occurrence tells the author to do, empty for a rule
	// whose registered sentence says the whole of it.
	//
	// It is per objection rather than per rule because that is the only place it
	// can be true: a rule whose remedy differs between two of its own violation
	// classes — reorder the block, versus the block cannot be reordered to make
	// this reference resolve — has no single sentence to register, and a
	// registered one would be wrong at whichever class it did not describe.
	advice string
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

// AuthoringDocument returns which authoring document the subject was written
// in — [AuthoringDocumentAggregate] or [AuthoringDocumentRelease] — or the empty
// string where the traversal that supplied the subject records none.
//
// It is recorded where a subject's site can sit in either document — a
// connection, or an attachment written on one, drawn by the developer in the
// Aggregate or by the operator in the Release — and it is taken from that site,
// which settled it, rather than read back off the file: a file names a path, and
// nothing in the path says what kind of document lives there.
//
// A boundary that files a finding under the author it belongs to reads it, the
// way a finding naming a document is filed by that document's kind; a boundary
// that only positions a finding reads the file and the locator instead.
func (o Objection) AuthoringDocument() string { return o.document }

// AuthoringDocumentCategory is the authoring surface a finding written in the
// given authoring document is filed under: the category that document's entity
// kind maps to, through cerrors.ClassifyEntityKind — the one mapping from an
// entity kind to the surface its faults belong to — so a fault in what the
// operator wrote on the Release reads as the operator's and a fault in what the
// developer wrote in the Aggregate as the developer's, whichever stage noticed it.
//
// A document outside the pair, the empty one included, is filed under the
// developer's surface, which is the mapping's answer for the Aggregate: every
// traversal whose site records no document positions its subject at a
// component's own block, in the Aggregate that component was written in. It is
// one function because every boundary filing an objection by its document reads
// it, and two copies of the default would agree only until one changed.
func AuthoringDocumentCategory(document string) cerrors.Category {
	if kind, named := AuthoringDocumentKind(document); named {
		if category, covered := cerrors.ClassifyEntityKind(string(kind)); covered {
			return category
		}
	}
	return cerrors.DeveloperError
}

// Advice returns what this occurrence tells the author to do, or the empty
// string for a rule whose registered sentence says the whole of it.
//
// It is read separately from the detail because it occupies a different slot in
// a composed message: the detail names the values the finding is about, and the
// advice names the edit. A boundary composing its own diagnostic around the
// rule's registered wording reads both — see [Objection.RenderedDetail] — and one
// taking the whole rendered message reads neither, because [ObjectionMessage] has
// already placed them.
func (o Objection) Advice() string { return o.advice }

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
//
// A boundary composing its own diagnostic this way places the rule's values and
// has to place its advice too, from [Objection.Advice] — the two are separate
// accessors because they land in separate parts of a sentence, and a boundary
// reading only this one drops the remedy for a rule that carries one.
func (o Objection) RenderedDetail() []string {
	return RenderDetail(o.detail...)
}

// RenderDetail spells detail values as a message reads them: each value quoted,
// with its caption ahead of it where it carries one. It is nil for no values.
//
// It is the spelling [Objection.RenderedDetail] returns, available to a caller
// that states a rule's finding without an objection to read it from — a command
// that reaches the same finding outside a check invocation, and must report it
// in the same words a build does. Such a caller builds the values with
// [Uncaptioned] and [Captioned], exactly as the rule does, and a detail list then
// has one spelling whichever of them reports it.
//
// It does not construct an objection. The seal on those is unchanged: a raised
// finding still comes only from a subject the framework supplied, and this
// renders values, never provenance.
func RenderDetail(values ...DetailValue) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, d := range values {
		out = append(out, d.render())
	}
	return out
}

// ArtifactEntryLocator returns the locator a document's artifacts entry is
// positioned at — the one a check raised over that entry carries — for a caller
// that reports the same finding without a subject to read the position from.
//
// It is assembled by the same two steps a subject's provenance is, so the two
// positions cannot come to differ. An entry name carrying the locator separator,
// as a filename does, positions the finding at the artifacts block rather than
// inside it, for the reason [authoredLocator] states.
func ArtifactEntryLocator(entry string) string {
	return authoredLocator(documentSpecSegments(authoredKeyArtifacts, entry)...)
}

// ObjectionMessage renders what one check raised as the text a diagnostic
// carries: the sentinel's registered wording, then the values the check
// supplied.
//
// It lives here, beside the objection, because the objection is the only thing
// it reads. A boundary converting an objection into its own diagnostic shape
// decides which of its fields the provenance lands on, and that decision differs
// per boundary — but the wording does not, and a second boundary composing it
// again would be free to compose it differently. One renderer is what makes
// "every rule reporting this way reports identically" a property rather than an
// intention, and the package that owns the objection is the only home every
// boundary can reach.
//
// The message opens with the sentinel's own registered text, so a rule's
// wording lives with the rule's identity and a second copy cannot drift from
// it. The values the check supplied follow it, because the registered text is
// one static string per rule and cannot carry them — so a message that were
// only that text would drop the very thing an author has to act on.
//
// That arrangement — the sentence, then the occurrence's values in parentheses —
// is the composition every diagnostic naming a condition follows, so this
// renders through [cerrors.ComposeMessage] rather than spelling it again. What
// this adds is the one thing an objection needs and that composition does not
// assume: a check's sentinel IS the rule, so its text is the rule's wording
// whether or not the rule carries a registered identity, and a rule whose
// identity is not yet registered still reads as a sentence rather than opening
// with a parenthesis. See [unregisteredCondition].
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
//
// # The advice slot
//
// The composition's third part is the advice for this occurrence, and it is filled
// from the objection rather than left empty. That the slot exists at all is the
// shared composition's own decision, taken on the ground that a sentence
// registered with advice in it is advice that has to be true at every site raising
// the condition — so advice belongs to the raise. A check IS the raise path, which
// is why the objection carries it and this places it: a rule whose remedy differs
// between its own violation classes has no registered sentence that could hold it,
// and stating the wrong remedy is worse than stating none.
//
// A rule supplying no advice composes exactly as it did before the slot was
// filled, so the slot costs such a rule nothing and moves no message it produced.
func ObjectionMessage(o Objection) string {
	condition := o.Sentinel()
	if _, registered := cerrors.MessageOf(condition); !registered {
		condition = unregisteredCondition{sentinel: condition}
	}
	return cerrors.ComposeMessage(condition, o.RenderedDetail(), o.Advice())
}

// unregisteredCondition presents a sentinel carrying no registered identity to
// the shared composition, so the composition has a sentence to open with.
//
// The composition reads a condition's sentence off the registered identity in
// its chain and contributes none when there is no identity to read. That is
// right for a diagnostic naming a condition — an error carrying no identity
// names no condition, so there is no registered wording to state — and it is not
// what an objection means. A check's sentinel is the rule itself, and a rule has
// wording from the moment it is written, so dropping it would leave the values
// alone inside a parenthesis with nothing saying which rule they belong to.
//
// So a sentinel already carrying a registered identity is handed over untouched
// and the composition reads it directly. One that is not is wrapped here, where
// the sentinel's own text becomes the sentence and both codes are empty because
// there is no registered condition to name. The wrapper never leaves
// [ObjectionMessage], so nothing reads those codes: it exists to answer one
// question the composition asks, and the answer is the rule's own words.
type unregisteredCondition struct{ sentinel error }

// Error returns the sentinel's own text, which is the wording the rule carries
// in the absence of a registered one.
func (u unregisteredCondition) Error() string { return u.sentinel.Error() }

// ErrorCode returns the empty string: there is no registered condition to name.
func (u unregisteredCondition) ErrorCode() string { return "" }

// ErrorShortCode returns the empty string, for the reason ErrorCode does.
func (u unregisteredCondition) ErrorShortCode() string { return "" }
