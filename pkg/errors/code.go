// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package errors

import (
	stderrors "errors"
	"fmt"
	"strings"
	"sync"
)

// sealed is the identity of a halting condition. It is declared by registering
// a message and both of the condition's identifiers together, and it is reached
// through an exported accessor standing beside the registration:
//
//	var errBudgetExceeded = Register("example/budget-exceeded", Meta{
//		ShortCode: "E0000",
//		Message:   "the declared budget was exceeded",
//	})
//
//	// ErrBudgetExceeded fires when …
//	func ErrBudgetExceeded() error { return errBudgetExceeded }
//
// The identity above is illustrative and names no package in this module,
// deliberately. An example naming a real declaration is a second copy of that
// declaration's value, and the two drift the moment either is edited. E0000 is
// the illustrative short code for the same reason and can never be a real one:
// short codes are handed out from E0001 upward, so nothing is ever registered
// under E0000. Do not "correct" it to a plausible-looking number — a plausible
// number is exactly what could also be somebody's real assignment.
//
// # Why the identity is a value no caller can construct
//
// Both the type and its fields are unexported, so the only way to obtain an
// identity is to call the accessor of the package that declares it. That is the
// whole point of the shape: an identity a caller could build for itself would
// let any package claim any condition without importing the package that owns
// it, and the owning package could then never move the condition without
// silently breaking a matcher it cannot see.
//
// The code carried here is an OUTPUT and never an input to matching. A caller
// reads it off an error with CodeOf; writing the same string back produces a
// string, not an identity, and there is no path from one to the other.
//
// # An identity carries nothing that differs between occurrences
//
// One identity value is shared by every occurrence of the condition it names,
// so it cannot express which file or which position produced a particular
// failure. That data belongs on the error instance — see CompilerError and its
// Source — and never on the identity. The distinction is the whole reason both
// objects exist: the identity answers "which rule halted", the instance answers
// "where".
type sealed struct {
	code      string
	shortCode string
	message   string
}

// Error returns the message the identity was registered with.
//
// The message travels on the identity rather than being looked up, so a caller
// wrapping it with fmt.Errorf("...: %w", err) renders that text, unchanged, at
// every enclosing frame. It can never be empty: an empty message renders as a
// dangling separator inside an enclosing wrap, which reads as a truncated
// diagnostic rather than as the defect it is, and registration refuses one.
func (s *sealed) Error() string { return s.message }

// ErrorCode returns the descriptive code the identity was registered under —
// the qualified, hyphenated one, such as `resolver/parameter-contract-violation`.
//
// It is a durable identifier of the condition itself, deliberately separate
// from the Go accessor that yields the identity, so the two carry their
// stability independently: the accessor can be renamed or moved without
// disturbing the code a condition was registered under.
//
// It is reached from Go through CodeOf or the CodedError interface, and from
// outside Go on the structured diagnostic the compiler-as-service daemon
// publishes, where it travels as a field of its own. That is what the separate
// identifier buys a consumer that does not link this package: it reads the code
// rather than recognising a condition by the wording of its message.
//
// It is not the code `aggrega` prints on a terminal. A printed diagnostic opens
// with its category and the condition's SHORT code, because a qualified,
// hyphenated code is wide enough on the longest conditions to push the message
// off a narrow terminal. The division holds for both surfaces: this code is the
// one a machine consumer matches on, the short code is the one a reader quotes.
func (s *sealed) ErrorCode() string { return s.code }

// ErrorShortCode returns the short code the identity was registered under — the
// compact one: the letter E and four digits.
//
// It names exactly the same condition ErrorCode names, in a form short enough
// to sit at the head of a diagnostic line without pushing the message off a
// narrow terminal. The two are not alternatives and neither replaces the other:
// the descriptive code says what the condition is at a glance and is what a
// machine consumer matches on, while the short code is what a reader can quote
// in a report, type into a search, and look up. Read whichever the surface
// needs; both are fixed once the condition is published.
//
// It is reached from Go through ShortCodeOf or the CodedError interface, the
// same way the descriptive code is.
func (s *sealed) ErrorShortCode() string { return s.shortCode }

// CodedError is an error that reports the identifiers of the halting condition
// it names. Every registered identity satisfies it.
//
// It is the interface to match with errors.As when a condition's identifiers
// are what is wanted rather than the identity itself — see CodeOf and
// ShortCodeOf, which are the usual way to ask.
type CodedError interface {
	error
	// ErrorCode returns the descriptive code of the condition the error names.
	ErrorCode() string
	// ErrorShortCode returns the short code of the condition the error names.
	ErrorShortCode() string
}

// CodeOf returns the code reported by the first error in err's chain that
// reports one, and whether any error in the chain did.
//
// It walks the chain, so the code survives arbitrary wrapping: a caller holding
// an error many frames above the site that raised it still reads the code of
// the condition underneath. An error chain carrying no registered identity
// reports no code, which is a fact about the chain rather than a failure.
func CodeOf(err error) (string, bool) {
	var coded CodedError
	if stderrors.As(err, &coded) {
		return coded.ErrorCode(), true
	}
	return "", false
}

// ShortCodeOf returns the short code reported by the first error in err's chain
// that reports one, and whether any error in the chain did.
//
// It is CodeOf's counterpart and walks the same chain for the same reason: the
// short code survives arbitrary wrapping, so a surface composing a diagnostic
// many frames above the site that raised the failure still names the condition
// underneath it. An error chain carrying no registered identity reports no
// short code, which is a fact about the chain rather than a failure.
//
// The two reads answer the same question in two forms and never disagree about
// which condition they found: both resolve the FIRST identity in the chain, so a
// caller reading one and then the other is describing one condition and not two.
func ShortCodeOf(err error) (string, bool) {
	var coded CodedError
	if stderrors.As(err, &coded) {
		return coded.ErrorShortCode(), true
	}
	return "", false
}

// MessageOf returns the sentence registered for the first condition in err's
// chain that carries one, and whether any error in the chain did.
//
// It is CodeOf's companion and walks the same chain for the same reason: the
// sentence survives arbitrary wrapping, so a surface composing a diagnostic
// many frames above the site that raised the failure still opens with the
// wording the condition was registered under.
//
// It asks a different question from err.Error(), and the difference is the
// whole reason it exists. err.Error() reports the OUTERMOST frame's text,
// which is whatever that frame chose to say; this reports the wording of the
// condition underneath it. A composer reading err.Error() and calling the
// result the condition's wording would be rendering its own wrap, and the two
// agreeing on the day it is written is exactly what would hide the divergence
// the day either is edited.
//
// An error chain carrying no registered identity reports no sentence, which is
// a fact about the chain rather than a failure.
func MessageOf(err error) (string, bool) {
	var coded CodedError
	if stderrors.As(err, &coded) {
		return coded.Error(), true
	}
	return "", false
}

// ComposeMessage renders a diagnostic in the one composition every condition's
// message follows: the condition's registered sentence first, the occurrence's
// own values after it in parentheses, and the advice for this occurrence last.
//
// It is the single implementation of that composition, and it is a function
// rather than a convention for a concrete reason: several composers each
// building the same shape from their own struct fields would be several copies
// of one condition's words, and such copies agree until one of them is edited.
// A composer that needs the shape calls this; a composer whose
// report groups many findings calls it for the header and appends its findings
// beneath, so the leading sentence and the parenthesised occurrence still have
// one definition.
//
// Every part is optional except the shape. A condition carrying no registered
// sentence contributes none, which is a fact about the chain rather than a
// failure — see MessageOf. An occurrence supplying no values gets no
// parentheses, so a sparse failure reads as a sentence rather than as a
// sentence trailing empty separators. Advice belongs to the raise path rather
// than to the identity, which is why it is an argument here and not a field on
// Meta: a sentence registered with advice in it is advice that has to be true
// at every site that raises the condition, and the site where it is wrong is
// the site a reader is most likely to reach.
//
// The sentence is read off the condition rather than passed as text, so a
// caller cannot supply a second copy of the wording by accident.
func ComposeMessage(condition error, values []string, advice string) string {
	var b strings.Builder
	if sentence, ok := MessageOf(condition); ok {
		b.WriteString(sentence)
	}
	if len(values) > 0 {
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		b.WriteString("(")
		b.WriteString(strings.Join(values, ", "))
		b.WriteString(")")
	}
	if advice != "" {
		if b.Len() > 0 {
			b.WriteString(". ")
		}
		b.WriteString(advice)
	}
	return b.String()
}

// Meta is the metadata declared beside an identity at its registration.
//
// It carries the two things every halting condition has of its own and that
// nothing else can supply, and each earns its place the same way:
//
//   - Message, the one sentence stating the condition. There is no source for
//     it but the declaration. It is the wording a diagnostic leads with where
//     the raise path composes one — see the field's own documentation for the
//     shape, and for which raise paths do not follow it yet.
//   - ShortCode, the compact identifier a reader quotes and looks up. It cannot
//     be derived from the descriptive code beside it — a short code is a number
//     handed out once and then fixed, precisely so that re-wording a condition
//     or moving it between packages does not change what a user has already
//     quoted — so, like the message, it is declared or it does not exist.
//
// The descriptive code is not here because it is the registration's own first
// argument rather than metadata about it. The two other properties a halting
// condition has are derived rather than declared, because neither is a property
// of the identity:
//
//   - Which authoring artifact is at fault is derived from the entity kind
//     involved in a particular failure — see ClassifyEntityKind. Declaring it
//     per identity would be a second home for a value that already has one.
//   - Which stage of the pipeline raised the condition is a property of the
//     occurrence, not of the identity: a single rule can be raised from more
//     than one stage, so any single declared value would be false for those.
//
// DeclaredAhead is the third field, and it is a different kind of thing from
// the two above: it does not describe the condition, it records a fact about
// the condition's relationship to the code around it — that nothing raises it
// yet, and which work will. It is here rather than in a list somewhere else so
// that it travels with the identity: a side list is a second home for the
// correspondence, and a second home is what drifts when a condition is renamed,
// moved between packages, or removed.
//
// A field is added here only when every identity has a source for it and
// something consumes it. A field populated for a minority is worse than an
// absent one, because it reads as a measured absence rather than an undecided
// question.
//
// DeclaredAhead satisfies that rule rather than bending it, and the reason is
// worth stating because at a glance it looks like the minority-populated field
// the rule refuses. Every identity has a source for it: the empty value is not
// a gap but an assertion — that the condition needs no such note, because
// something raises it. And something consumes it: the published reference
// admits a condition that nothing raises only when this field names the work
// that will, so the field is read on every generation rather than only
// displayed.
type Meta struct {
	// Message is the text the identity's Error method returns: one sentence
	// stating the condition, in the words a reader of a diagnostic sees.
	//
	// It is the single home of that wording, and the composition rule gives
	// that home a shape: a diagnostic composed through ComposeMessage OPENS
	// with this sentence and appends whatever the occurrence supplies after it
	// — the document, the authored key, the offending value, the advice.
	//
	// The shape is the rule composition follows, and NOT yet a property of
	// every printed diagnostic. A raise path that builds its own message text
	// rather than composing one can print this sentence later in the line, or
	// leave it to a trailing cause line, or omit it — measured, several do. So
	// read this as where the wording lives and what composition does with it,
	// never as a guarantee about every line a user sees. The split is forced
	// by what each side can carry. This
	// sentence is one static string per condition, so it cannot name the
	// occurrence; the occurrence's values mean nothing without the sentence
	// that says which rule they are about. Composing the wording a second time
	// from the occurrence's own fields would leave two copies of one
	// condition's words, and the copy is what drifts — an agreeing copy most
	// of all, because the agreement is what hides the divergence until one
	// side is edited.
	//
	// Read the sentence off an error with MessageOf, which walks the chain the
	// way CodeOf does.
	//
	// It must not be empty: an empty sentence renders as a dangling separator
	// inside an enclosing wrap, and registration refuses one.
	Message string

	// ShortCode is the condition's compact identifier: the letter E followed by
	// exactly four decimal digits.
	//
	// # What it is for, given the condition already has a code
	//
	// The descriptive code says what the condition is; this one is short enough
	// to print. A diagnostic wants its identifier at the head of the line, where
	// the reader is already looking, and the descriptive code is too wide for
	// that position: it is package-qualified and kebab-case, so the longest ones
	// run to many tens of characters — most of a narrow terminal before the
	// message has begun. The width is described rather than counted on purpose:
	// a figure here is a second copy of what the declarations below already
	// determine, and it would go stale the day a longer code is registered.
	// Four digits fit
	// anywhere and stay quotable — a user can read one out, type it into a
	// search, or paste it into a report, which is the whole job.
	//
	// It is opaque by construction, and that is the price paid for the width: a
	// four-digit number says nothing about the condition on its own, so it is
	// usable only alongside the correspondence between short codes and the
	// conditions they name. That correspondence is what the declarations here
	// establish, and every surface that reports a short code carries it beside
	// the condition rather than on its own.
	//
	// # One flat space, not one per category
	//
	// Short codes are globally unique across every category. They have to be: a
	// condition has no category of its own. Which category a diagnostic prints
	// is derived per occurrence from the entity kind involved — see
	// ClassifyEntityKind — so one condition raised against two kinds prints two
	// categories, and a per-category number would have to be two numbers for one
	// condition. One flat space means a short code identifies a condition
	// without needing its category to disambiguate it.
	//
	// # Allocation: what is enforced here, and what is convention
	//
	// Registration ENFORCES, by panicking at package initialisation: the value
	// is present, it has the shape above, no other condition already holds it,
	// and it is not a number the retirement record records as retired. Those are
	// the properties a program can check about itself.
	//
	// A number is never reused, and BOTH halves of that are enforced. A number a
	// live condition holds is refused by the registry's own index; a number a
	// retired condition held is refused against the retirement record this
	// package declares, which records every one of them and what each named. So
	// a retired condition's number stays retired, and a user quoting it from an
	// old report is never sent to a different condition.
	//
	// # A tombstone is not a gap check, and the two are one edit apart
	//
	// Never-reusing GUARANTEES permanent gaps in the sequence, so no gap check
	// exists and none should be added: a no-gaps check would refuse exactly the
	// state the rule produces.
	//
	// What makes the retirement refusal a different thing is the direction of
	// the evidence. A gap is the ABSENCE of a record and settles nothing — a
	// number missing from the sequence may have been retired, and may equally be
	// one nobody has reached yet, and the sequence cannot tell those apart. A
	// tombstone is a POSITIVE record of the first case, written when the
	// condition was withdrawn by whoever knew which case it was. So the refusal
	// declines the numbers the record names and accepts every other gap, which
	// is why it can be enforced at all.
	//
	// An implementation refusing "any number below the highest that is not
	// currently registered" would be a gap check wearing a tombstone's name. Do
	// not write one however tempting the symmetry looks: it would refuse the
	// legal, permanent state the numbering is designed to produce.
	//
	// Everything else is CONVENTION, upheld by whoever adds a condition and by
	// review, because none of it is checkable from inside the program:
	//
	//   - Numbers are handed out monotonically from E0001. Monotonic in
	//     ALLOCATION order — the order conditions were added — and never in sort
	//     order: a condition added later takes the next free number rather than
	//     being inserted among its neighbours. So a package's numbers say when
	//     its conditions were declared relative to every other package's, and
	//     nothing about how they sort or read together.
	//   - The next free number is one greater than the highest already allocated,
	//     counting the retired ones. Because the numbering is one flat space
	//     shared by every package, the highest is read off the whole declared set
	//     rather than off the neighbours of the condition being added — and the
	//     retirement record is part of that set, so a number retired above every
	//     live one is still retired.
	//
	// It must not be empty, and registration refuses an empty one for the same
	// reason it refuses an empty descriptive code: a condition with nothing to
	// report identifies itself to nobody.
	ShortCode string

	// DeclaredAhead names the work that will raise this condition, and is set
	// only on a condition nothing raises yet.
	//
	// Empty is the ordinary value and is an assertion rather than a gap: it
	// says the condition needs no such note because something already raises
	// it. Nearly every condition leaves it empty.
	//
	// # What it is for
	//
	// Declaring a condition before the code that raises it is legitimate — an
	// identity is reserved so that the number is fixed and the wording settled
	// before the raise path exists. What is not legitimate is a published
	// reference that promises a check the build cannot perform. This field is
	// how the two are told apart: a condition nothing raises reaches the
	// reference only when this field is set, and it reaches it carrying this
	// text, so a reader who looks the condition up is told plainly that it does
	// not fire yet and what would make it fire.
	//
	// It is NOT a suppression. Setting it does not remove the condition from
	// anything that reports on the declared set — a condition marked here is
	// named as declared-ahead every time the reference is generated, so the set
	// of them stays countable and visible rather than becoming a quiet corner.
	//
	// # It is published, so it is written for a reader of the documentation
	//
	// This text is rendered onto the public error reference verbatim. Write it
	// in the words that page is written in: what the condition will report and
	// what has to exist before it can, in terms of the product's own surfaces.
	// It names no internal planning artifact, and it is not a note to whoever
	// maintains this package — that note belongs in the comment above the
	// declaration, where nothing publishes it.
	//
	// # It cannot be acquired by neglect
	//
	// Registration refuses a value that is present but says nothing — one that
	// is entirely spaces, say. A marker that names no work is exactly the
	// suppression this field must not become: it would take a condition off the
	// unraised report while telling a reader of the reference nothing at all.
	// Setting the field deliberately, with real text, is therefore the only way
	// a condition can carry it.
	DeclaredAhead string
}

// shortCodeLen and shortCodePrefix are the shape of a short code: the letter E
// followed by exactly four decimal digits.
//
// The width is fixed rather than minimal so that short codes sort and align as
// text. Zero-padding is what makes a one-digit number and a four-digit one line
// up in a printed column, and order correctly in any listing that sorts them as
// strings, which every listing does.
const (
	shortCodeLen    = 5
	shortCodePrefix = 'E'
)

// wellFormedShortCode reports whether s has the shape of a short code.
//
// It checks the shape and nothing beyond it. Whether a particular value is one a
// condition SHOULD have been given — the next free number, rather than a reused
// or arbitrarily chosen one — is an allocation question, and allocation is
// convention rather than enforcement for the reasons recorded on Meta.ShortCode.
func wellFormedShortCode(s string) bool {
	if len(s) != shortCodeLen || s[0] != shortCodePrefix {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// Both indexes hold the same identities, keyed by the two identifiers a
// condition carries, because each key has to be checked for a collision on its
// own. Two conditions could otherwise agree on a short code while their
// descriptive codes differ, which the descriptive index alone cannot see.
var (
	registryMu     sync.RWMutex
	registry       = map[string]*sealed{}
	shortCodeIndex = map[string]*sealed{}
)

// retirement is one retired short code: a number that named a condition, names
// none now, and will never name another.
//
// It is a POSITIVE record, and that is the whole of its value. A number missing
// from the sequence says nothing on its own — it may have been retired, and it
// may equally be one nobody has reached yet — so only a declaration here tells
// the two apart. That is why the refusal in [Register] consults this record and
// never the shape of the sequence; [Meta.ShortCode] carries the distinction in
// full.
type retirement struct {
	// shortCode is the retired number, in the shape a live registration
	// declares: the letter E and exactly four decimal digits.
	shortCode string

	// note says what the code named and what became of the condition, in one
	// sentence.
	//
	// It is published verbatim on the error reference, beneath the codes a build
	// can still report, so a reader who quotes a number off an old report is
	// told what it named rather than left holding a code that resolves to
	// nothing. Write it in the words that page is written in, as one sentence
	// on one line, reading on from a lead sentence that has already said the
	// code is retired.
	note string
}

// retiredShortCodes records every short code that has named a condition and no
// longer does.
//
// # Why it is authored rather than derived
//
// Nothing a program can observe about itself distinguishes a retired number
// from one never handed out: both are simply absent from the registry, and a
// deletion leaves no trace a later run can read. So the record cannot be
// computed — it has to be written when the condition is withdrawn, which is
// also the only moment anyone knows what the number named and why it is retired.
//
// # It has one home, and this is it
//
// The refusal in [Register] reads this record, and the generator that composes
// the published error reference reads this same declaration to publish the
// retired codes beside the live ones. Neither keeps a copy: a second list to
// hold in step is exactly what would drift, and the drift would be invisible
// because the two would agree until the day one of them was edited.
//
// # Adding to it
//
// Whoever retires a condition deletes its registration and adds a line here in
// the same change. The two halves are one act: a deletion without a line here
// frees the number for reuse, and reuse is the one thing the numbering promises
// cannot happen.
var retiredShortCodes = []retirement{
	{"E0157", "It named an Aggregate key — a component, feature, subsystem or nested-module " +
		"instance name — that did not match the authored-name pattern. Every one of those key " +
		"surfaces is constrained to that same spelling by the Aggregate schema, which refuses " +
		"the document while validating its envelope and reports the offending key with a line " +
		"and column; that condition's code is `E0215`."},
	{"E0159", "It named a target reference whose dot-separated segments did not all match the " +
		"instance-name pattern, and no build reported it before the condition was withdrawn."},
	{"E0217", "It named a schema-validation failure that could not be reported against a particular " +
		"field, and no build reported it before the condition was withdrawn."},
	{"E0219", "It named a cyclic `extends` chain, reported by a second chain walk the schema-merge " +
		"library kept beside its combining step; one walk reports every cyclic chain now, and its " +
		"code is `E0202`."},
	{"E0234", "It named a Recipe source that is an empty string, reported while rendering through the " +
		"compose provider; one condition covering every provider reports that now, and its code is `E0251`."},
	{"E0242", "It named a Recipe source that is an empty string, reported while rendering through the " +
		"Helm provider; one condition covering every provider reports that now, and its code is `E0251`."},
}

// retiredIndex keys [retiredShortCodes] for the lookup [Register] performs.
//
// It is a separate variable rather than a lazily built one so that a record the
// program cannot serve is refused at package initialisation, before any input
// is read, the way every other registration defect is. [Register] refers to it,
// so every registration in this package — and every package that imports it —
// is initialised after it: a registration reaching the retirement check can
// never find the record half-built.
var retiredIndex = indexRetirements(retiredShortCodes)

// indexRetirements keys a retirement record by short code, and panics on a
// record that would leave a hole in the refusal it feeds.
//
// Each refusal is one a silently-admitted entry would turn into a promise the
// program does not keep:
//
//   - A malformed short code matches nothing a registration can declare, so the
//     number it was meant to protect would be quietly free again.
//   - A note carrying no text would publish a bare retired code with nothing to
//     tell a reader who quotes it, which is the outcome the note exists to
//     prevent.
//   - A note spanning more than one line ends the list item it publishes into
//     and spills the rest onto the page as prose.
//   - A number recorded twice means two answers to one lookup, and the second
//     would be the one nobody wrote.
//
// The record is walked in declaration order, so a record carrying more than one
// defect names the same one on every run.
func indexRetirements(record []retirement) map[string]string {
	out := make(map[string]string, len(record))
	for _, r := range record {
		if !wellFormedShortCode(r.shortCode) {
			panic(fmt.Sprintf("errors: the retirement record declares the malformed short code %q; "+
				"a short code is the letter E followed by exactly four decimal digits, and a value of "+
				"another shape matches no registration, so the number it was meant to keep retired is free again",
				r.shortCode))
		}
		if strings.TrimSpace(r.note) == "" {
			panic(fmt.Sprintf("errors: the retirement record declares the short code %q with no note; "+
				"the note is published beside the code a reader quotes, so an empty one publishes a "+
				"retired number and tells that reader nothing", r.shortCode))
		}
		if strings.ContainsAny(r.note, "\r\n") {
			panic(fmt.Sprintf("errors: the retirement record's note for short code %q spans more than "+
				"one line, which ends the list item it publishes into and spills the rest onto the page "+
				"as prose; write the note as a single sentence", r.shortCode))
		}
		if _, dup := out[r.shortCode]; dup {
			panic(fmt.Sprintf("errors: the retirement record declares the short code %q twice, so a "+
				"lookup keyed on it has two answers and takes whichever was written second", r.shortCode))
		}
		out[r.shortCode] = r.note
	}
	return out
}

// Register declares the halting condition named by code, with the metadata in
// meta, and returns its identity. The call stands as a package-level
// declaration, with the accessor that publishes it directly beside:
//
//	var errBudgetExceeded = Register("example/budget-exceeded", Meta{
//		ShortCode: "E0000",
//		Message:   "the declared budget was exceeded",
//	})
//
//	// ErrBudgetExceeded fires when …
//	func ErrBudgetExceeded() error { return errBudgetExceeded }
//
// The identity above is illustrative and names no package in this module,
// deliberately. An example naming a real declaration is a second copy of that
// declaration's value, and the two drift the moment either is edited. E0000 is
// the illustrative short code for the same reason and can never be a real one:
// short codes are handed out from E0001 upward, so nothing is ever registered
// under E0000. Do not "correct" it to a plausible-looking number — a plausible
// number is exactly what could also be somebody's real assignment.
//
// The returned type is unexported, and that is the design rather than an
// oversight. A caller binds the result with type inference and never names it,
// which is precisely what leaves the identity unconstructible anywhere but
// here. Publishing it through a function rather than an exported variable is
// the other half: a variable an importer can see is a variable an importer can
// reassign, and a reassigned identity silently stops matching everywhere.
//
// # A condition carries two identifiers, and they are shaped for two jobs
//
// Both are chosen so that distinct conditions stay distinct: two conditions
// sharing either identifier become two identities a consumer outside Go cannot
// tell apart, and the second registration is refused outright.
//
// The first argument is the DESCRIPTIVE code. It is qualified by the declaring
// package as well as the rule, which keeps two same-named rules in different
// packages apart, and it is kebab-case in keeping with every other descriptive
// identifier the platform defines. It is the identifier to match on: it says
// what the condition is, so a consumer branching on it reads as what it means.
//
// Meta.ShortCode is the SHORT code — the letter E and four digits. It names the
// same condition in a form narrow enough to print at the head of a diagnostic
// line, which a descriptive code's own width does not fit. The ShortCode field's
// own documentation carries the width reasoning, so this does not restate it. It is
// deliberately the one platform-defined identifier that is not kebab-case,
// because its whole purpose is width at the head of every diagnostic, and it is
// opaque as a consequence: it is readable only against the correspondence
// between short codes and conditions. Prefer the descriptive code wherever
// width does not bind.
//
// # Refusals
//
// Register panics, rather than reporting an error, on each of the conditions
// below. All are defects in how the program is assembled rather than anything a
// user can cause, and registration runs at package initialisation, so a panic
// fires on every run and every test before any input is read — it cannot reach
// a release. An error return would be the weaker choice here for a mechanical
// reason: the call stands as a declaration whose result is bound to the
// identity, so a returned error has nowhere to go.
//
//   - An empty code. The zero value is not an identity, and admitting it would
//     leave a condition with nothing to report on the wire.
//   - An empty message. The identity would then render as the empty string,
//     which is the silent shape a diagnostic can least afford.
//   - A code already registered, whether or not the two messages agree. Two
//     registrations for one code means two identities a consumer outside Go
//     cannot tell apart; permitting the agreeing case would leave a hole that
//     opens silently the day one of the messages is edited.
//   - An empty short code, for the same reason an empty code is refused: the
//     condition would print no identifier on the surface the short code exists
//     to serve.
//   - A malformed short code — anything but E followed by exactly four decimal
//     digits. A value of another shape is one no reader can recognise as an
//     identifier at all, and one nothing keying on the shape can look up.
//   - A short code already registered. This is checked separately from the code
//     above rather than folded into it: the two identifiers are independent, so
//     a duplicated short code is entirely possible between conditions whose
//     descriptive codes differ, and that is precisely the case a single check
//     would miss.
//   - A short code the retirement record records as retired. The number named a
//     condition once and never names another, so admitting it would send a
//     reader quoting it off an old report to a different condition — the one
//     thing the numbering promises cannot happen. The refusal says the code is
//     RETIRED rather than in use, and repeats what it named, so a reader learns
//     which of the two reuse rules they hit and does not go looking for a live
//     declaration that does not exist. Meta.ShortCode records why this is a
//     tombstone check and not the gap check that must never be written.
//   - A Meta.DeclaredAhead that is present but says nothing — whitespace alone.
//     That field's whole value is that it cannot be acquired by neglect: a
//     marker naming no work would take a condition off the unraised report and
//     put it on the published reference with nothing to tell a reader, which is
//     the suppression the field exists to prevent. Absent is the ordinary
//     value and is always accepted; present-and-empty is the shape refused.
//
// Meta.DeclaredAhead is checked here and deliberately not stored on the
// identity. Nothing at run time reads it — it is read from the declaration
// itself, by the generator that composes the published reference — so keeping a
// copy on the identity would be a second home for a value that already has one.
// Refusing a malformed declaration and recording a declared value are separate
// jobs, and this function does the first for a field it does not do the second
// for.
//
// What Register does NOT check is whether a short code is the NEXT one — the
// number above the highest already allocated rather than an arbitrary free one.
// That much is allocation, and it is convention rather than enforcement. Reuse
// is the half that IS enforced, on both of its sides: a number a live condition
// holds and a number the retirement record records as retired are each refused
// above. Meta.ShortCode records which half is which, and why the tombstone
// refusal is not the gap check that would be wrong. Nor does it check whether a
// condition carrying no Meta.DeclaredAhead is in fact raised: that is a
// property of the whole module rather than of one declaration, so nothing
// inside a single registration can see it.
func Register(code string, meta Meta) *sealed {
	registryMu.Lock()
	defer registryMu.Unlock()

	if code == "" {
		panic("errors: Register called with an empty code")
	}
	if meta.Message == "" {
		panic(fmt.Sprintf("errors: Register called with an empty message for code %q", code))
	}
	if _, dup := registry[code]; dup {
		panic(fmt.Sprintf("errors: Register called twice for code %q", code))
	}
	if meta.ShortCode == "" {
		panic(fmt.Sprintf("errors: Register called with an empty short code for code %q", code))
	}
	if !wellFormedShortCode(meta.ShortCode) {
		panic(fmt.Sprintf("errors: Register called with a malformed short code %q for code %q; "+
			"a short code is the letter E followed by exactly four decimal digits",
			meta.ShortCode, code))
	}
	if meta.DeclaredAhead != "" && strings.TrimSpace(meta.DeclaredAhead) == "" {
		panic(fmt.Sprintf("errors: Register called with a blank declared-ahead note for code %q; "+
			"a condition nothing raises reaches the published reference only by naming the work that "+
			"will raise it, so a note carrying no text would publish the condition and tell a reader "+
			"nothing; write the note, or leave the field unset and let the condition be reported as unraised",
			code))
	}
	if prev, dup := shortCodeIndex[meta.ShortCode]; dup {
		panic(fmt.Sprintf("errors: Register called with short code %q for code %q, "+
			"which is already registered for code %q; short codes are handed out from E0001 upward "+
			"and never reused, so take the next number above the highest already allocated",
			meta.ShortCode, code, prev.code))
	}
	// The retirement check runs AFTER the live-collision check above, and the
	// order matters only in a tree that is already wrong. A retired number holds
	// no live registration, so at most one of the two can fire — unless the
	// retirement record names a number a condition still holds, which is an
	// authoring defect in the record itself. In that case the collision refusal
	// is the more useful of the two, because it names the condition holding the
	// number and so says which of the two declarations to correct.
	if note, retired := retiredIndex[meta.ShortCode]; retired {
		panic(fmt.Sprintf("errors: Register called with the retired short code %q for code %q; "+
			"a retired number is never handed to a different condition, so a short code quoted from "+
			"an old report is never sent somewhere else. %s Take the next number above the highest "+
			"already allocated, counting the retired ones",
			meta.ShortCode, code, note))
	}

	// Neither index is written until every refusal above has passed, so a
	// refused registration leaves both untouched and the two cannot disagree
	// about which conditions exist.
	id := &sealed{code: code, shortCode: meta.ShortCode, message: meta.Message}
	registry[code] = id
	shortCodeIndex[meta.ShortCode] = id

	return id
}

// Condition describes a registered halting condition: the two identifiers it
// carries and the sentence every diagnostic naming it opens with.
//
// # It is a description, and deliberately not an identity
//
// The lookups below return one of these rather than the identity itself, and
// that is what keeps the sealing intact. An identity is still obtainable only
// from the accessor of the package that declares it: this value carries the
// condition's published strings, so a caller holding one can report the
// condition to a reader and can not match on it, wrap it, or hand it anywhere an
// error is expected.
//
// So the rule stated on the identity type is untouched — the code remains an
// OUTPUT and never an input to matching. What the lookups add is the other
// direction of READING: a reader who quoted a code off a terminal can be told
// which condition it names. That is answering a question about a code, not
// resolving a code into an identity, and the two are different acts on different
// values.
type Condition struct {
	// Code is the descriptive code the condition is registered under — the
	// qualified, hyphenated one a consumer outside Go matches on.
	Code string

	// ShortCode is the compact code the condition is registered under — the
	// letter E and four digits, the one a reader quotes off a terminal.
	ShortCode string

	// Message is the sentence the condition registers: the wording a composed
	// diagnostic opens with, before any occurrence's own values.
	Message string
}

// ConditionByShortCode returns the condition registered under shortCode, and
// whether any condition is.
//
// It is the read a surface performs for someone holding a code a build printed.
// The short code is the identifier that reaches a terminal, so it is the one a
// reader arrives with; a value under no registration reports false, which is a
// fact about the argument rather than a failure — see RetiredShortCode for the
// case where the number named a condition once.
func ConditionByShortCode(shortCode string) (Condition, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	id, ok := shortCodeIndex[shortCode]
	if !ok {
		return Condition{}, false
	}
	return conditionOf(id), true
}

// ConditionByCode returns the condition registered under the descriptive code,
// and whether any condition is.
//
// It is ConditionByShortCode's counterpart for the other identifier, and it
// exists because the two identifiers reach two different readers: the short code
// opens a printed diagnostic, while the descriptive code travels as a field of
// its own on the structured diagnostic the compiler-as-service daemon publishes.
// A surface that resolved only one of them would be silent for whoever was
// handed the other.
//
// Like its counterpart it returns a description and never an identity, so this
// is not the string-to-identity path the sealing forbids.
func ConditionByCode(code string) (Condition, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	id, ok := registry[code]
	if !ok {
		return Condition{}, false
	}
	return conditionOf(id), true
}

// conditionOf projects an identity onto its published description. It is the one
// site that decides which of an identity's fields are published, so the two
// lookups above cannot come to disagree about that.
func conditionOf(id *sealed) Condition {
	return Condition{Code: id.code, ShortCode: id.shortCode, Message: id.message}
}

// RetiredShortCode returns the note recorded against shortCode when the
// retirement record holds one, and whether it does.
//
// It is what turns the numbering's promise into something a reader can act on. A
// number that named a condition and no longer does resolves to nothing through
// ConditionByShortCode, and a reader quoting it off an old report cannot
// otherwise tell a retired number from one they mistyped. This reads the same
// record [Register] consults to refuse a reissue, so what a reader is told and
// what the program refuses come from one declaration.
func RetiredShortCode(shortCode string) (string, bool) {
	note, ok := retiredIndex[shortCode]
	return note, ok
}
