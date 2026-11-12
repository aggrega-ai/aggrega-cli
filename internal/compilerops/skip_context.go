// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compilerops

import (
	stderrors "errors"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// SkipContextError is a resolution or validation failure carrying the catalog
// files the tolerant load refused. It DECORATES the failure rather than
// replacing it: Unwrap returns the failure itself, so errors.Is and errors.As
// still reach the condition the failure raised and a registered condition's
// short code still survives to the reader.
//
// It is a named type rather than an fmt wrap because a diagnostic formatter
// renders a failure from the outermost categorised envelope it can reach and
// discards every frame above it. A note appended by an fmt wrap therefore
// reaches the reader only while the failure happens to carry no envelope, and
// disappears the moment one is introduced beneath it — silently, on the exact
// path where the note is the only text naming what the author wrote wrong. A
// named type gives the formatter something to detect, so the note reaches the
// reader whatever shape the failure it decorates has.
//
// promoted is the refused file the record singled out as the reason this
// failure happened, and nil otherwise. It is what the reader meets first: an
// author whose document is missing a required field is told that, not told that
// a reference to the entity that document would have provided could not be
// bound. The failure it explains is still reported, inside the note, because it
// is the context that says why the document was needed at all.
//
// The whole record is held rather than only its error, because a surface that
// points a reader at an authoring contract has to name the KIND the refused
// document declared — and a failure about a reference names the kind that
// reference sits in, which is a different document.
type SkipContextError struct {
	err      error
	promoted *catalog.SkippedEntity
	note     string
}

// Error renders the failure the reader is meant to meet first, followed by the
// note. That is the promoted refusal where one was singled out, and the
// decorated failure otherwise; the other of the two is inside the note either
// way, so nothing is dropped.
//
// The promoted refusal is rendered exactly as the note renders a refusal it
// lists, short code and all. A surface reading this whole string has no
// formatter of its own composing a head, so leaving the code off here would
// leave that reader with a diagnostic they cannot look up — while the same
// refusal, merely listed rather than promoted, carries it.
func (e *SkipContextError) Error() string {
	if e.promoted != nil {
		return refusalReason(e.promoted.Err) + e.note
	}
	return e.err.Error() + e.note
}

// Unwrap returns the decorated failure, so errors.Is and errors.As traverse
// into it and the sentinel the failure raised stays matchable at the boundary.
// Promotion changes what the reader meets first; it does not change what a
// caller matches on.
func (e *SkipContextError) Unwrap() error { return e.err }

// Leading returns the failure the reader is meant to meet first: the promoted
// refusal where one was singled out, and the decorated failure otherwise.
//
// A surface that renders a failure through its own formatter needs this rather
// than Error, because it renders the leading failure in whatever shape that
// failure's own type calls for and appends Note itself. Error composes the two
// already, for a surface that wants the whole text in one string.
func (e *SkipContextError) Leading() error {
	if e.promoted != nil {
		return e.promoted.Err
	}
	return e.err
}

// Note returns the text appended after the leading failure: the closure-
// membership sentence, and the refusals the report did not lead with.
func (e *SkipContextError) Note() string { return e.note }

// PromotedRefusal returns the refused catalog file the record singled out as
// the reason this failure happened, and whether one was singled out.
//
// A surface that points a reader at an authoring contract reads it to name the
// document the reader must open. Without it the pointer is derived from the
// failure, which is about a REFERENCE — so it names the kind the reference sits
// in rather than the kind of the document that was refused, and sends an author
// to the contract for a file that is correct.
func (e *SkipContextError) PromotedRefusal() (catalog.SkippedEntity, bool) {
	if e.promoted == nil {
		return catalog.SkippedEntity{}, false
	}
	return *e.promoted, true
}

// AnnotateSkipContext annotates an in-closure resolution / validation failure
// with the catalog files the load refused, so a tolerated skip never hides WHY
// an entity the Release reaches went missing. A skipped file whose header named
// the kind+name is reported with that identity; a file too malformed to peek is
// reported by path. No-op when err is nil, or when there is neither a refusal
// nor a local-catalog note to report.
//
// localCatalogNote is the workspace's account of where local entities are read
// from, which the resolved workspace composes and a caller passes in already
// rendered. It answers the other half of "nothing resolved": a reference may
// name an entity no file declares anywhere, or one declared in a directory this
// workspace does not read, and only the second is something the reader fixes by
// moving a file or naming a directory. It is reported beside refusals of files
// that declare something else, because the two explain different things and a
// workspace can be in both states at once. An empty string reports nothing —
// which is what a reader who declared that this workspace has no local catalog
// gets, since advice about where local entities are read from would contradict
// that declaration.
//
// 🔴 Whether it is reported is decided by LocalCatalogAdvice, the one predicate
// every surface that carries the account reads, and the guard is here rather than
// at the callers for the reason the whole file is here: a caller's own failure is
// not always one the account explains. It is reported only where the reference
// resolved to nothing, and not where a document the catalog read and refused
// declares what the reference names — that document was found where the catalog
// reads, so moving it is the wrong remedy.
//
// CAUSE BEFORE CONSEQUENCE, where the cause can be named exactly. A refused
// file never enters the catalog, so what fails is the attempt to resolve a
// reference to what that file declared — a consequence of the refusal. Where the
// record singles out exactly one refusal declaring the identity the failing
// reference names, that refusal leads the report and the resolution failure
// moves into the note beneath it. Where it cannot — no refusal declares that
// identity, or more than one does — the report is exactly what it would be with
// no record at all: the failure leads, and every refusal is listed beneath it
// for the reader to weigh. Naming the wrong file as the cause would be worse
// than naming none, because a reader acts on it.
//
// The note states the distinction the tolerant load draws, because the reader
// meeting the note is the one who has to act on it: a refused file OUTSIDE this
// Release's closure is tolerated and never fails a build, so a failure that is
// only listed alongside refusals is explained only by a refusal naming something
// this Release reaches. That is stated rather than left implicit — a reader who
// does not know which class is which cannot tell a cause from a bystander. Where
// a refusal IS promoted the distinction is settled rather than stated: the
// promoted file declares the identity the Release reached, which is what made it
// promotable.
//
// The failure stays in the returned error's chain rather than being rendered
// into text, so a registered condition's identity survives to the reader and
// errors.Is still matches it at the command boundary — promoted or not.
//
// Each reason is rendered as its whole cause chain, not just its outermost
// frame: the outermost frame typically says only which step refused the file
// ("unmarshal Component"), while the frame beneath it is the one naming the
// constraint the file violated — the only part an author can act on.
//
// It lives beside IsResolutionFailure rather than on any one front end, because
// the two answer halves of one question: which failures a skip record can
// explain, and what the reader is told once it does. Every surface that reports
// a resolution failure — a terminal, a tool response — reaches this one home, so
// an author meets the same account of what went wrong wherever they are reading.
//
// A file the load skipped because it declares a kind no catalog holds is a
// different record, and it is reported only where it explains this failure. It
// never entered the catalog under any kind, so it is listed beneath the failure
// when it declares the name the failing reference asks for — whatever kind that
// reference was collected as — and is otherwise left out, exactly as the load
// left it out. It is never promoted: the kind it declares names no identity, so
// the failure keeps leading and the file is what the note points at. Where one
// is listed, the account of where local entities are read from is not, because
// the file is already where the catalog reads.
func AnnotateSkipContext(err error, skipped []catalog.SkippedEntity, localCatalogNote string) error {
	if err == nil {
		return err
	}
	return annotateSkips(err, skipped, localCatalogAdviceFor(err, skipped, localCatalogNote), releaseSkipWording)
}

// AnnotateEntitySkipContext is AnnotateSkipContext for a check of one entity on
// its own: err is a failure to find, in the catalog, what that entity refers to,
// carrying that reference as a closure.UnresolvedEntityError exactly as a failure
// the closure walk raises does.
//
// The account is the one AnnotateSkipContext gives, cause before consequence:
// where exactly one refused file declares the identity the reference names, that
// refusal leads and the failure moves into the note beneath it; where none or
// several do, the failure leads and every refusal is listed beneath it. Only the
// sentences that would speak of a Release say instead what this check has: an
// entity, and what it refers to. The workspace's account of where it reads local
// entities is not carried, because this check reads only the catalog directories
// it is handed.
//
// It returns err unchanged when err is nil, and when the load refused nothing and
// no file of a kind no catalog holds declares the name the reference asks for.
func AnnotateEntitySkipContext(err error, skipped []catalog.SkippedEntity) error {
	if err == nil {
		return err
	}
	return annotateSkips(err, skipped, "", entitySkipWording)
}

// skipWording is the sentences of a skip account that name what reached the
// identity a failure could not resolve. Each opens with the line break and
// indentation it is printed at.
type skipWording struct {
	// promoted follows the promoted refusal's note and introduces the failure it
	// explains.
	promoted string
	// others introduces the refusals listed after a promoted one.
	others string
	// listed introduces every refusal where none was promoted.
	listed string
}

// releaseSkipWording is the wording for a failure raised while resolving what a
// Release reaches.
var releaseSkipWording = skipWording{
	promoted: "\n  This Release reaches that identity, so what failed is the attempt to resolve the reference to it:",
	others: "\n  note: other catalog files were skipped at load too. A refusal outside this Release's closure is" +
		"\n  tolerated and never fails a build:",
	listed: "\n  note: catalog files skipped at load. A refusal outside this Release's closure is tolerated and never fails a build," +
		"\n  so this failure is explained only by a refusal naming something this Release reaches:",
}

// entitySkipWording is the wording for a failure raised while checking one
// entity on its own, where no Release is being resolved.
var entitySkipWording = skipWording{
	promoted: "\n  This entity refers to that identity, so what failed is the attempt to resolve the reference to it:",
	others:   "\n  note: other catalog files were skipped at load too:",
	listed: "\n  note: catalog files skipped at load. This failure is explained only by a refusal naming something this entity" +
		"\n  refers to:",
}

// annotateSkips is the account AnnotateSkipContext and AnnotateEntitySkipContext
// give, over the failure err, the load's record skipped and the already-decided
// local-catalog note, in the wording words.
func annotateSkips(err error, skipped []catalog.SkippedEntity, localCatalogNote string, words skipWording) error {
	refused, misdeclared := partitionSkipped(err, skipped)
	if len(refused) == 0 && len(misdeclared) == 0 && localCatalogNote == "" {
		return err
	}
	var b strings.Builder
	cause, promoted := refusalExplaining(err, refused)
	if promoted {
		b.WriteString("\n  note: " + RefusedDocumentNote + ".")
		b.WriteString(words.promoted)
		fmt.Fprintf(&b, "\n    %s", indentContinuations(cerrors.CauseChain(err), "    "))
		writeMisdeclared(&b, misdeclared)
		if rest := refusalsBesides(refused, cause); len(rest) > 0 {
			b.WriteString(words.others)
			writeRefusals(&b, rest)
		}
		b.WriteString(localCatalogNote)
		return &SkipContextError{err: err, promoted: &cause, note: b.String()}
	}
	writeMisdeclared(&b, misdeclared)
	if len(refused) > 0 {
		b.WriteString(words.listed)
		writeRefusals(&b, refused)
	}
	b.WriteString(localCatalogNote)
	return &SkipContextError{err: err, note: b.String()}
}

// referenceOf returns the reference a failure carries — the decoration the
// closure walk raises for one unresolved reference, which a check resolving a
// reference outside the walk raises too — and whether err carries one. It is the
// one place the decoration is read.
func referenceOf(err error) (*closure.UnresolvedEntityError, bool) {
	var unresolved *closure.UnresolvedEntityError
	if !stderrors.As(err, &unresolved) || unresolved == nil {
		return nil, false
	}
	return unresolved, true
}

// partitionSkipped splits the load's record into the files it refused while
// indexing them, in the record's order, and the files of a kind no catalog holds
// that declare the name the reference err failed on — the only ones of that kind
// this failure is reported with. A failure carrying no reference is explained by
// none of them.
func partitionSkipped(err error, skipped []catalog.SkippedEntity) (refused, misdeclared []catalog.SkippedEntity) {
	refused = make([]catalog.SkippedEntity, 0, len(skipped))
	for _, s := range skipped {
		if !s.KindNotHeld() {
			refused = append(refused, s)
		}
	}
	if len(refused) == len(skipped) {
		return skipped, nil
	}
	return refused, misdeclaredFor(err, skipped)
}

// misdeclaredFor returns the files in skipped that declare, under a kind no
// catalog holds, the name the reference err failed on, or nil where err carries
// no reference.
func misdeclaredFor(err error, skipped []catalog.SkippedEntity) []catalog.SkippedEntity {
	ref, ok := referenceOf(err)
	if !ok {
		return nil
	}
	return catalog.UnheldKindDocumentsFor(skipped, ref.URI, ref.KindHint)
}

// misdeclaredSentence is the note naming files that declare the name a failing
// reference asks for under a kind no catalog holds, split where the multi-line
// rendering breaks it. Both renderings are composed from it, so a terminal and
// a single-line message say the same thing in the same words.
func misdeclaredSentence(n int) (head, tail string) {
	if n == 1 {
		return "a catalog file declares the name this reference asks for under a kind a catalog does not hold",
			"so the load skipped it"
	}
	return "catalog files declare the name this reference asks for under a kind a catalog does not hold",
		"so the load skipped them"
}

// writeMisdeclared renders the note naming each file that declares the name the
// failing reference asks for under a kind no catalog holds, or nothing for none.
func writeMisdeclared(b *strings.Builder, misdeclared []catalog.SkippedEntity) {
	if len(misdeclared) == 0 {
		return
	}
	head, tail := misdeclaredSentence(len(misdeclared))
	b.WriteString("\n  note: " + head + ",")
	b.WriteString("\n  " + tail + ":")
	writeRefusals(b, misdeclared)
}

// MisdeclaredDocumentsNote returns the note naming each file in skipped that
// declares, under a kind no catalog holds, the name the reference uri asks for —
// in the multi-line shape the workspace's account of where it reads local
// entities takes, opening with a line break — or the empty string where none
// does. kindHint is the hint the reference was collected with, read only to
// split a recipe reference the way its kind composes it.
//
// It is what a surface reporting a lookup that matched nothing appends where
// LocalCatalogAdvice withholds that account because such a file declares the
// name: the account would send the reader to move a file the catalog already
// reads, and this names the file and what is wrong with it instead.
func MisdeclaredDocumentsNote(skipped []catalog.SkippedEntity, uri, kindHint string) string {
	var b strings.Builder
	writeMisdeclared(&b, catalog.UnheldKindDocumentsFor(skipped, uri, kindHint))
	return b.String()
}

// MisdeclaredDocumentsLine is MisdeclaredDocumentsNote on one line, for a
// surface whose message is a single line: `note: <sentence>: <refusal>; …`,
// each refusal stated once with its short code in its head and its document
// named as report spells a path. It returns the empty string where no file
// matches.
func MisdeclaredDocumentsLine(skipped []catalog.SkippedEntity, uri, kindHint string, report func(path string) string) string {
	return misdeclaredLine(catalog.UnheldKindDocumentsFor(skipped, uri, kindHint), report)
}

// MisdeclaredFailureLine is MisdeclaredDocumentsLine for the reference a
// resolution failure carries, read off the failure rather than out of its text.
// A failure carrying no reference is explained by no such file, and gets the
// empty string.
func MisdeclaredFailureLine(err error, skipped []catalog.SkippedEntity, report func(path string) string) string {
	return misdeclaredLine(misdeclaredFor(err, skipped), report)
}

// misdeclaredLine renders the one-line note over the files already matched.
func misdeclaredLine(misdeclared []catalog.SkippedEntity, report func(path string) string) string {
	if len(misdeclared) == 0 {
		return ""
	}
	if report == nil {
		report = func(p string) string { return p }
	}
	head, tail := misdeclaredSentence(len(misdeclared))
	var b strings.Builder
	b.WriteString("note: " + head + ", " + tail + ":")
	for _, s := range misdeclared {
		fmt.Fprintf(&b, " %s;", RefusedDocumentLine(report(s.Path), s.Err))
	}
	return b.String()
}

// RefusedDocumentNote is the sentence that follows a refused document's fault
// where a report leads with it: why a document refused at load explains a
// reference to what it declares resolving to nothing.
//
// It is shared rather than written at each surface that leads with a refusal,
// because the multi-line note a terminal reads and the one-line message a
// connection reads teach the same fact, and two copies of one sentence agree
// only until one of them is edited.
const RefusedDocumentNote = "this file was skipped at load, so the catalog holds nothing under the identity it declares"

// LocalCatalogAdvice returns note — the workspace's account of where it reads
// local entities, as the resolved workspace composes it — where that account is
// the remedy for a lookup of uri that resolved nothing, and the empty string
// where it is not.
//
// It is the one predicate for the account, and every surface that carries it
// decides through it: a failure the closure walk raised reaches it through
// AnnotateSkipContext, and a by-name lookup that matched nothing calls it
// directly. A caller calls it only for a lookup that resolved nothing, which is
// the one failure the account can explain at all.
//
// The account says the document is not where the catalog reads. That is false
// for a document the catalog read and refused, and every such document is in the
// refusal record skipped — so where a refusal there declares the identity uri
// names, or a file there of a kind no catalog holds declares the name it asks
// for, the document was found where the catalog reads, moving it is the wrong
// remedy, and nothing is returned. Deciding it from the record rather than from
// the configured directories is what keeps it free of a second walk of those
// directories: the record is the part of that walk that already happened.
//
// kindHint is the kind hint the reference was collected with, in the vocabulary
// catalog.SoleRefusalFor reads. A reference whose hint maps to no kind can match
// no refusal, so for it the account is returned whenever the lookup resolved
// nothing.
func LocalCatalogAdvice(note string, skipped []catalog.SkippedEntity, uri, kindHint string) string {
	if note == "" || refusalDeclares(skipped, uri, kindHint) {
		return ""
	}
	return note
}

// localCatalogAdviceFor is LocalCatalogAdvice for a failure the closure walk
// raised: it establishes that the failure resolved nothing, and reads the
// reference off the failure.
//
// A walk that could not record the reference it failed on raises the bare
// condition, and with no reference there is no refusal to match, so the account
// is returned as it is for a reference no refusal declares.
func localCatalogAdviceFor(err error, skipped []catalog.SkippedEntity, note string) string {
	if !namesAnUnresolvedEntity(err) {
		return ""
	}
	ref, ok := referenceOf(err)
	if !ok {
		return note
	}
	return LocalCatalogAdvice(note, skipped, ref.URI, ref.KindHint)
}

// namesAnUnresolvedEntity reports whether err failed because a reference named
// an entity nothing resolved — the one failure the workspace's account of where
// it reads local entities explains.
//
// It is the narrow question, not the broad one, and the condition is what
// answers it. A failure raised while resolving what a Release reaches can be
// about the shape of a document rather than about finding one — a Release that
// does not match its kind's schema stops the same step — and a lookup can find
// the entity and still fail: bytes a registry now serves that no longer match
// their pin, or a document's own files that cannot be read. The walk decorates
// every lookup failure with the reference it was resolving, so the decoration
// says which reference failed and never that nothing was found; the
// unresolvable-reference condition is raised only where every tier answered that
// it holds no such entity. Matching the decoration would give a reader told that
// their pinned bytes changed the advice to move a file.
//
// The condition is matched through its exported accessor rather than by its
// registered identifier, which keeps the match working if that identifier is
// ever re-registered.
func namesAnUnresolvedEntity(err error) bool {
	return stderrors.Is(err, closure.ErrUnresolvableURI())
}

// refusalExplaining reports the one refused catalog file that explains err, and
// whether exactly one does.
//
// It reads the reference off the failure rather than out of its rendered text.
// A resolution failure carries the reference it could not resolve as a field, so
// the match is against the identity the refused document declares for itself —
// an identity match, not a search for a name inside a message.
//
// A failure carrying no reference at all cannot be explained by any single
// refusal, so it reports false: a validation failure over an entity that DID
// load names no missing entity, and blaming a refusal for it would be a guess.
func refusalExplaining(err error, skipped []catalog.SkippedEntity) (catalog.SkippedEntity, bool) {
	ref, ok := referenceOf(err)
	if !ok {
		return catalog.SkippedEntity{}, false
	}
	return SoleRefusal(skipped, ref.URI, ref.KindHint)
}

// SoleRefusal reports the one refused catalog file declaring the identity a
// reference names, and whether exactly one does — catalog.SoleRefusalFor, with
// the refusal that leads a report held to carrying a reason of its own.
//
// A matched refusal carrying no reason is declined. There would be nothing to
// lead the report with, and leading with a note whose subject is absent reads
// worse than the report it replaced.
//
// Every surface that leads a report with a refusal asks this, so a terminal and
// a connection single out the same file for the same reference.
func SoleRefusal(skipped []catalog.SkippedEntity, uri, kindHint string) (catalog.SkippedEntity, bool) {
	cause, ok := catalog.SoleRefusalFor(skipped, uri, kindHint)
	if !ok || cause.Err == nil {
		return catalog.SkippedEntity{}, false
	}
	return cause, true
}

// refusalDeclares reports whether any refusal in skipped declares the identity
// uri names, or any file there of a kind no catalog holds declares the name it
// asks for.
//
// It asks catalog.SoleRefusalFor of each record on its own, which is that
// function's own match applied one record at a time rather than a second
// statement of it. The question here is whether any document declaring the
// identity was read, so two refused documents declaring it answer yes, where
// promotion — which needs exactly one — declines. A file of a kind no catalog
// holds was read where the catalog reads too, and catalog.UnheldKindDocumentsFor
// is the one match for it.
func refusalDeclares(skipped []catalog.SkippedEntity, uri, kindHint string) bool {
	for _, s := range skipped {
		if _, ok := catalog.SoleRefusalFor([]catalog.SkippedEntity{s}, uri, kindHint); ok {
			return true
		}
	}
	return len(catalog.UnheldKindDocumentsFor(skipped, uri, kindHint)) > 0
}

// KindHintFor returns the kind hint catalog.SoleRefusalFor reads for an entity
// kind as a document declares it, or the empty string for a kind it holds no
// hint for.
//
// A surface that knows the kind a document declares, rather than the site a
// reference was collected at, holds no hint to pass. The hint is the entity kind
// catalog.EntityKindForCatalogKind pairs with the document's kind — every entity
// kind is also the hint its collection sites carry — so the pairing keeps its one
// home, and a kind it pairs with nothing matches no refusal, which is the answer
// the matcher gives for such a reference anyway.
func KindHintFor(kind string) string {
	return catalog.EntityKindForCatalogKind(kind)
}

// refusalsBesides returns the refusals other than the promoted one, keeping the
// record's order. The promoted file is already the head of the report, so
// repeating it in the note would say the same thing twice.
func refusalsBesides(skipped []catalog.SkippedEntity, promoted catalog.SkippedEntity) []catalog.SkippedEntity {
	rest := make([]catalog.SkippedEntity, 0, len(skipped))
	for _, s := range skipped {
		if s.Path == promoted.Path {
			continue
		}
		rest = append(rest, s)
	}
	return rest
}

// indentContinuations prefixes every line of s after the first with indent,
// preserving whatever indentation those lines already carry. A failure's own
// text can be several lines — a remediation sentence sits on its own — and
// placing such a block under a note means its continuation lines have to move
// with it, or they read as belonging to the level above.
func indentContinuations(s, indent string) string {
	return strings.ReplaceAll(s, "\n", "\n"+indent)
}

// refusalReason renders one refusal's whole cause chain with its condition's
// short code in the head, which is the single home of how a refused file's
// reason reads. A refusal is rendered in two places — promoted ahead of the
// failure it explains, or listed beneath it — and the two must not differ, or a
// reader would meet the same refusal in two shapes depending on whether the
// record could single it out.
func refusalReason(err error) string {
	reason := cerrors.CauseChain(err)
	if code, ok := cerrors.ShortCodeOf(err); ok {
		reason = shortCodeInHead(reason, code)
	}
	return reason
}

// writeRefusals renders one bullet per refused file: its declared identity where
// the header yielded one, its path otherwise, then the condition it raised.
func writeRefusals(b *strings.Builder, skipped []catalog.SkippedEntity) {
	for _, s := range skipped {
		reason := refusalReason(s.Err)
		switch {
		case s.Kind != "" && s.Name != "":
			fmt.Fprintf(b, "\n    - %s %q (%s): %s", s.Kind, s.Name, s.Path, reason)
		default:
			fmt.Fprintf(b, "\n    - %s: %s", s.Path, reason)
		}
	}
}

// shortCodeInHead puts a condition's short code inside the bracketed head a
// rendered diagnostic already opens with, beside the category — the one
// position the error reference teaches a code appears in, so a refused file
// reported inside a note names its condition exactly as it would when it is
// the whole diagnostic.
//
// A reason that opens with no bracketed head is returned unchanged rather than
// gaining a second, differently-shaped presentation of the same identifier. So
// is one whose head already carries two tokens: the head is category-then-code
// and nothing else, so a space inside it means the code is already there and
// the function is idempotent.
func shortCodeInHead(reason, code string) string {
	if !strings.HasPrefix(reason, "[") {
		return reason
	}
	end := strings.Index(reason, "]")
	if end < 0 || strings.Contains(reason[:end], " ") {
		return reason
	}
	return reason[:end] + " " + code + reason[end:]
}
