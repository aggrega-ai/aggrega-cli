// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package errors_test

import (
	stderrors "errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// The identities below are declared and published exactly as a consuming
// package writes them: a registration bound to an unexported value, and an
// accessor standing beside it. Declaring them here means the registrations run
// at package initialisation, which is the shape being tested rather than a
// convenience of the test.
//
// Every code value in this file is distinct. The registry is process-wide, so a
// repeated value would be a duplicate registration and would panic at init.
const (
	codeCyclicExtension        = "cyclic-extension-subject"
	codeSiblingCyclicExtension = "sibling-cyclic-extension-subject"
	codeDuplicate              = "duplicate-subject"
	codeEmptyMessage           = "empty-message-subject"
	codeEmptyShortCode         = "empty-short-code-subject"
	codeMalformedShortCode     = "malformed-short-code-subject"
	codeDuplicateShortCode     = "duplicate-short-code-subject"
	codeBlankDeclaredAhead     = "blank-declared-ahead-subject"
	codeSetDeclaredAhead       = "set-declared-ahead-subject"

	codeBlankRulePlaceReason    = "blank-rule-place-reason-subject"
	codePlaceOverAuthoredInput  = "place-over-authored-input-subject"
	codePlaceEvaluatedElsewhere = "place-evaluated-elsewhere-subject"
	codePlaceNotARule           = "place-not-a-rule-subject"
)

// The short codes this file registers, distinct for the same reason the codes
// above are: the registry indexes both identifiers, so a repeated short code is
// a duplicate registration and panics at init just as a repeated code does.
//
// They are NOT allocations, and they sit in a high band so that nobody reads one
// as a number taken off the published mapping. This test binary registers only
// what these test files declare — the package under test registers nothing of
// its own — so a test value has to be distinct from its siblings here and from
// nothing else.
const (
	shortCyclicExtension        = "E9211"
	shortSiblingCyclicExtension = "E9212"
	shortDuplicate              = "E9213"
	shortEmptyMessage           = "E9214"
	shortMalformedSubject       = "E9215"
	shortDuplicateSubject       = "E9216"
	shortBlankDeclaredAhead     = "E9217"
	shortSetDeclaredAhead       = "E9218"

	shortBlankRulePlaceReason    = "E9222"
	shortPlaceOverAuthoredInput  = "E9224"
	shortPlaceEvaluatedElsewhere = "E9225"
	shortPlaceNotARule           = "E9226"
)

// The short code a malformed-value refusal is probed with, and the reason it is
// malformed: the shape is the letter E and exactly four decimal digits, so a
// value carrying the right prefix and the wrong width is refused. It is written
// as one constant rather than inline so the test asserts against the value the
// refusal was given rather than against a second copy of it.
const malformedShortCode = "E42"

// The two messages below carry the SHAPE the property under test needs: two
// rules that share one exported name across two packages stay two identities
// with two messages. They reproduce no real declaration, deliberately — a copy
// of a live message here would be a second home for that wording, and it would
// go stale silently the day the real one moved, without anything failing.
const (
	messageCyclicExtension        = "the first subject's cyclic-chain sentence"
	messageSiblingCyclicExtension = "the second subject's cyclic-chain sentence"
)

var (
	identityCyclicExtension = cerrors.Register(codeCyclicExtension,
		cerrors.Meta{ShortCode: shortCyclicExtension, Message: messageCyclicExtension})
	identitySiblingCyclicExtension = cerrors.Register(codeSiblingCyclicExtension,
		cerrors.Meta{ShortCode: shortSiblingCyclicExtension, Message: messageSiblingCyclicExtension})
	identityDuplicate = cerrors.Register(codeDuplicate,
		cerrors.Meta{ShortCode: shortDuplicate, Message: "the first registration of this code"})
)

func errCyclicExtension() error        { return identityCyclicExtension }
func errSiblingCyclicExtension() error { return identitySiblingCyclicExtension }

// The registered identity satisfies both the error interface a caller matches
// against and the interface that reports the code. These assertions are the
// compile-time half of the guarantee: they fail to build rather than to run if
// either method is ever dropped or given a receiver the returned value does not
// have.
var (
	_ error              = identityCyclicExtension
	_ cerrors.CodedError = identityCyclicExtension
	_ error              = identityDuplicate
)

func TestIdentity_ErrorReturnsTheRegisteredMessageByteForByte(t *testing.T) {
	t.Parallel()
	if got := errCyclicExtension().Error(); got != messageCyclicExtension {
		t.Errorf("Error() = %q; want %q", got, messageCyclicExtension)
	}
}

// TestIdentity_IsMatchesThroughTwoWrapLayers is the instrument for the property
// a caller depends on: an identity stays matchable however deeply the error is
// wrapped on its way up. Two layers rather than one, because a single layer
// would also pass for a comparison that only ever inspects the outermost error.
func TestIdentity_IsMatchesThroughTwoWrapLayers(t *testing.T) {
	t.Parallel()

	inner := fmt.Errorf("reading the extends chain: %w", errCyclicExtension())
	outer := fmt.Errorf("resolving the aggregate: %w", inner)

	if !stderrors.Is(outer, errCyclicExtension()) {
		t.Errorf("errors.Is failed to match the identity through two wrap layers; chain: %v", outer)
	}
	if stderrors.Is(outer, errSiblingCyclicExtension()) {
		t.Errorf("errors.Is matched a different identity through the same chain; chain: %v", outer)
	}

	// A third layer, to establish that the match is depth-independent rather
	// than passing at exactly the depth the test happens to build.
	deepest := fmt.Errorf("building the graph: %w", outer)
	if !stderrors.Is(deepest, errCyclicExtension()) {
		t.Errorf("errors.Is failed to match the identity through three wrap layers; chain: %v", deepest)
	}
}

// TestIdentity_AccessorYieldsOneSharedValue pins what makes the accessor form
// work at all: every call returns the same identity, not a fresh equal-looking
// one. A constructor returning a new value each time would leave errors.Is
// matching nothing, because the standard comparison is on the value itself.
func TestIdentity_AccessorYieldsOneSharedValue(t *testing.T) {
	t.Parallel()

	if errCyclicExtension() != errCyclicExtension() {
		t.Fatalf("two calls to one accessor returned different identities")
	}
	wrapped := fmt.Errorf("resolving the aggregate: %w", errCyclicExtension())
	if !stderrors.Is(wrapped, errCyclicExtension()) {
		t.Errorf("an identity raised through one call did not match the value another call yields")
	}
}

// TestIdentity_CodeIsReadableThroughWraps is the instrument for the wire half
// of the split: the code is an output, readable from any error in the chain
// rather than only from the identity itself, and it equals the code the
// identity was registered under.
func TestIdentity_CodeIsReadableThroughWraps(t *testing.T) {
	t.Parallel()

	inner := fmt.Errorf("reading the extends chain: %w", errCyclicExtension())
	outer := fmt.Errorf("resolving the aggregate: %w", inner)

	got, ok := cerrors.CodeOf(outer)
	if !ok {
		t.Fatalf("no code was readable from a chain carrying a registered identity: %v", outer)
	}
	if got != codeCyclicExtension {
		t.Errorf("CodeOf = %q; want the code the identity was registered under, %q", got, codeCyclicExtension)
	}

	// The same read, through errors.As on the reporting interface directly,
	// because that is the shape a consumer outside this package writes when it
	// wants the reporting error rather than only the string.
	var coded cerrors.CodedError
	if !stderrors.As(outer, &coded) {
		t.Fatalf("errors.As found no code-reporting error in the chain: %v", outer)
	}
	if coded.ErrorCode() != codeCyclicExtension {
		t.Errorf("ErrorCode() = %q; want %q", coded.ErrorCode(), codeCyclicExtension)
	}

	// A chain carrying no registered identity reports no code. That is a fact
	// about the chain, so it is reported rather than guessed at.
	if got, ok := cerrors.CodeOf(stderrors.New("an ordinary error")); ok {
		t.Errorf("CodeOf reported %q for a chain carrying no identity", got)
	}
	if _, ok := cerrors.CodeOf(nil); ok {
		t.Errorf("CodeOf reported a code for a nil error")
	}
}

// TestIdentity_RegisteredSentenceIsReadableThroughWraps covers the accessor a
// surface composing a diagnostic reads: it wants the sentence the CONDITION was
// registered with, not the text of whatever frame it happens to be holding.
//
// The distinction is what the test is for. Both reads below run against the same
// chain, and they disagree — Error() reports the outermost wrap and MessageOf
// reports the registered sentence underneath it — so a composer that reached for
// Error() would render its own wrap and present it as the condition's wording.
func TestIdentity_RegisteredSentenceIsReadableThroughWraps(t *testing.T) {
	t.Parallel()

	inner := fmt.Errorf("reading the extends chain: %w", errCyclicExtension())
	outer := fmt.Errorf("resolving the aggregate: %w", inner)

	got, ok := cerrors.MessageOf(outer)
	if !ok {
		t.Fatalf("no sentence was readable from a chain carrying a registered identity: %v", outer)
	}
	if got != messageCyclicExtension {
		t.Errorf("MessageOf = %q; want the sentence the identity was registered with, %q", got, messageCyclicExtension)
	}
	if outer.Error() == got {
		t.Errorf("the outermost frame's text and the registered sentence are the same string (%q), so this "+
			"test cannot distinguish the two reads it exists to distinguish", got)
	}

	// Read off the bare identity, which is the shape a caller holding the
	// sentinel itself passes.
	if got, ok := cerrors.MessageOf(errCyclicExtension()); !ok || got != messageCyclicExtension {
		t.Errorf("MessageOf(identity) = %q, %v; want %q, true", got, ok, messageCyclicExtension)
	}

	// Two identities sharing an exported accessor name in two packages carry two
	// sentences, and the read must not collapse them.
	if got, _ := cerrors.MessageOf(fmt.Errorf("merging: %w", errSiblingCyclicExtension())); got != messageSiblingCyclicExtension {
		t.Errorf("MessageOf = %q; want the merger identity's own sentence %q", got, messageSiblingCyclicExtension)
	}

	// A chain carrying no registered identity reports no sentence, the same fact
	// CodeOf reports about the same chain.
	if got, ok := cerrors.MessageOf(stderrors.New("an ordinary error")); ok {
		t.Errorf("MessageOf reported %q for a chain carrying no identity", got)
	}
	if _, ok := cerrors.MessageOf(nil); ok {
		t.Errorf("MessageOf reported a sentence for a nil error")
	}
}

// TestIdentity_ForgingTheCodeStringYieldsNoIdentity is the runtime half of the
// asymmetry the split exists for: the code travels outward and never back. The
// compile-time half — that the identity type cannot be named, its field cannot
// be set, and the accessor cannot be reassigned — is enforced in the sealing
// gate, which is where a guarantee that must not merely be asserted belongs.
func TestIdentity_ForgingTheCodeStringYieldsNoIdentity(t *testing.T) {
	t.Parallel()

	forged := stderrors.New(codeCyclicExtension)
	if stderrors.Is(forged, errCyclicExtension()) {
		t.Errorf("an error built from the code string matched the identity, so the code is an input to matching")
	}
	if stderrors.Is(errCyclicExtension(), forged) {
		t.Errorf("the identity matched an error built from its own code string")
	}
}

// TestIdentity_RegisteredMessageSurvivesWrapping pins the property that makes
// the registered message the user-visible text: every enclosing frame renders it
// unchanged, so moving a message into a registration moves no output.
func TestIdentity_RegisteredMessageSurvivesWrapping(t *testing.T) {
	t.Parallel()

	inner := fmt.Errorf("reading the extends chain: %w", errCyclicExtension())
	outer := fmt.Errorf("resolving the aggregate: %w", inner)

	want := "resolving the aggregate: reading the extends chain: " + messageCyclicExtension
	if got := outer.Error(); got != want {
		t.Errorf("wrapped text = %q; want %q", got, want)
	}
}

// TestIdentity_MessageIsNeverEmpty pins the one forbidden answer. An empty
// message renders inside an enclosing wrap as a dangling separator, which reads
// as a truncated diagnostic rather than as the defect it is. Registration
// refuses one at the door, so no identity can carry it.
func TestIdentity_MessageIsNeverEmpty(t *testing.T) {
	t.Parallel()

	if errCyclicExtension().Error() == "" {
		t.Fatalf("an identity rendered the empty string")
	}
	wrapped := fmt.Errorf("resolving the aggregate: %w", errCyclicExtension()).Error()
	if strings.HasSuffix(wrapped, ": ") {
		t.Errorf("wrapped text ends in a dangling separator: %q", wrapped)
	}
}

// TestIdentity_SameAccessorNameInTwoPackagesStaysTwoIdentities holds the
// separation an identity gets from being a distinct registered value rather
// than from anything about its name. Two conditions reached through accessors
// sharing one exported name in two packages are distinct conditions, so
// errors.Is must not match across the pair and each must keep its own sentence.
//
// It is also where the naming constraint bites: a code names its condition and
// carries no qualifier, so two conditions cannot be told apart by where they
// are declared and each has to be named distinctly on its own. The two codes
// registered here are distinct for that reason, and registration refuses a
// duplicate outright.
func TestIdentity_SameAccessorNameInTwoPackagesStaysTwoIdentities(t *testing.T) {
	t.Parallel()

	if errCyclicExtension() == errSiblingCyclicExtension() {
		t.Fatalf("the two accessors yield one identity, so they are one condition")
	}
	if got := errCyclicExtension().Error(); got != messageCyclicExtension {
		t.Errorf("first identity Error() = %q; want %q", got, messageCyclicExtension)
	}
	if got := errSiblingCyclicExtension().Error(); got != messageSiblingCyclicExtension {
		t.Errorf("second identity Error() = %q; want %q", got, messageSiblingCyclicExtension)
	}

	wrapped := fmt.Errorf("resolving the aggregate: %w", errCyclicExtension())
	if stderrors.Is(wrapped, errSiblingCyclicExtension()) {
		t.Errorf("errors.Is matched across two distinct identities")
	}
}

func TestRegister_PanicsOnADuplicateCode(t *testing.T) {
	t.Parallel()

	v := panicValue(func() {
		cerrors.Register(codeDuplicate, cerrors.Meta{
			ShortCode: shortDuplicate,
			Message:   "a second registration of the same code",
		})
	})
	if v == nil {
		t.Fatalf("registering an already-registered code did not panic")
	}
	if msg := fmt.Sprint(v); !strings.Contains(msg, codeDuplicate) {
		t.Errorf("panic %q does not name the duplicated code %q", msg, codeDuplicate)
	}
}

func TestRegister_PanicsOnAnEmptyCode(t *testing.T) {
	t.Parallel()

	v := panicValue(func() {
		cerrors.Register("", cerrors.Meta{
			ShortCode: shortMalformedSubject,
			Message:   "a message for no identity",
		})
	})
	if v == nil {
		t.Fatalf("registering an empty code did not panic")
	}
}

func TestRegister_PanicsOnAnEmptyMessage(t *testing.T) {
	t.Parallel()

	v := panicValue(func() {
		cerrors.Register(codeEmptyMessage, cerrors.Meta{ShortCode: shortEmptyMessage})
	})
	if v == nil {
		t.Fatalf("registering an empty message did not panic")
	}
	if msg := fmt.Sprint(v); !strings.Contains(msg, codeEmptyMessage) {
		t.Errorf("panic %q does not name the offending code %q", msg, codeEmptyMessage)
	}
	// The refused registration must not have landed: a code rejected at the
	// door is unregistered, so registering it properly still succeeds. If the
	// refusal had left a half-written entry behind, this second call would be
	// refused as a duplicate.
	//
	// It also proves the SHORT-code index was left untouched, which is the half a
	// single-index check would miss: the refused call above claimed
	// shortEmptyMessage, so re-using it here is refused as a duplicate short code
	// if the refusal wrote either index before finishing its checks.
	if v := panicValue(func() {
		cerrors.Register(codeEmptyMessage, cerrors.Meta{
			ShortCode: shortEmptyMessage,
			Message:   "a message this time",
		})
	}); v != nil {
		t.Errorf("a refused registration left one of the two indexes occupied; re-registering panicked with %v", v)
	}
}

// TestRegister_PanicsOnAnEmptyShortCode covers the same door the empty-code
// refusal covers, on the other identifier. A condition admitted without a short
// code would print no identifier on the one surface the short code exists to
// serve, and it would leave a hole in the published mapping that nothing else
// reports.
func TestRegister_PanicsOnAnEmptyShortCode(t *testing.T) {
	t.Parallel()

	v := panicValue(func() {
		cerrors.Register(codeEmptyShortCode, cerrors.Meta{Message: "a message with no short code"})
	})
	if v == nil {
		t.Fatalf("registering an empty short code did not panic")
	}
	if msg := fmt.Sprint(v); !strings.Contains(msg, codeEmptyShortCode) {
		t.Errorf("panic %q does not name the offending code %q", msg, codeEmptyShortCode)
	}
}

// TestRegister_PanicsOnAMalformedShortCode pins the shape rather than only the
// presence. A value of another shape is one the published mapping cannot key and
// a reader cannot recognise as an identifier at all, so admitting it would put a
// condition on the wire under something that is not a short code.
//
// The probe carries the right prefix and the wrong width, which is the near miss
// a check on the leading letter alone would admit.
func TestRegister_PanicsOnAMalformedShortCode(t *testing.T) {
	t.Parallel()

	v := panicValue(func() {
		cerrors.Register(codeMalformedShortCode, cerrors.Meta{
			ShortCode: malformedShortCode,
			Message:   "a message with a short code of the wrong shape",
		})
	})
	if v == nil {
		t.Fatalf("registering the malformed short code %q did not panic", malformedShortCode)
	}
	msg := fmt.Sprint(v)
	if !strings.Contains(msg, malformedShortCode) {
		t.Errorf("panic %q does not name the malformed short code %q", msg, malformedShortCode)
	}
	if !strings.Contains(msg, codeMalformedShortCode) {
		t.Errorf("panic %q does not name the code %q it was declared for, so a reader cannot find the declaration",
			msg, codeMalformedShortCode)
	}
}

// TestRegister_PanicsOnADuplicateShortCode is the refusal a single index cannot
// provide. The two identifiers are independent, so two conditions can collide on
// the short code while their descriptive codes differ — and that is exactly the
// pair a duplicate-code check alone reads as two distinct conditions.
//
// The probe therefore registers a code that is NOT a duplicate, so the only
// refusal available is the short-code one. A test reusing a duplicated code as
// well would pass on the wrong check.
func TestRegister_PanicsOnADuplicateShortCode(t *testing.T) {
	t.Parallel()

	v := panicValue(func() {
		cerrors.Register(codeDuplicateShortCode, cerrors.Meta{
			ShortCode: shortCyclicExtension,
			Message:   "a distinct condition claiming a short code already taken",
		})
	})
	if v == nil {
		t.Fatalf("registering the already-registered short code %q under a fresh code did not panic",
			shortCyclicExtension)
	}
	msg := fmt.Sprint(v)
	if !strings.Contains(msg, shortCyclicExtension) {
		t.Errorf("panic %q does not name the duplicated short code %q", msg, shortCyclicExtension)
	}
	// Both sides of the collision are named, because a reader holding only one
	// of them has to search the tree for the other.
	if !strings.Contains(msg, codeDuplicateShortCode) || !strings.Contains(msg, codeCyclicExtension) {
		t.Errorf("panic %q does not name both conditions in the collision (%q claiming, %q holding)",
			msg, codeDuplicateShortCode, codeCyclicExtension)
	}
}

// TestIdentity_ShortCodeIsReadableThroughWraps is ShortCodeOf's half of the wire
// contract, and it is the same property CodeOf carries: the short code is an
// output, readable from any error in the chain rather than only from the identity
// itself, and it equals what the identity was registered under.
//
// The two reads are checked against ONE chain on purpose. They answer the same
// question in two forms, so a chain from which they resolved different conditions
// would make either answer meaningless.
func TestIdentity_ShortCodeIsReadableThroughWraps(t *testing.T) {
	t.Parallel()

	inner := fmt.Errorf("reading the extends chain: %w", errCyclicExtension())
	outer := fmt.Errorf("resolving the aggregate: %w", inner)

	got, ok := cerrors.ShortCodeOf(outer)
	if !ok {
		t.Fatalf("no short code was readable from a chain carrying a registered identity: %v", outer)
	}
	if got != shortCyclicExtension {
		t.Errorf("ShortCodeOf = %q; want the short code the identity was registered under, %q",
			got, shortCyclicExtension)
	}

	// The same read through errors.As on the reporting interface, which is the
	// shape a consumer outside this package writes.
	var coded cerrors.CodedError
	if !stderrors.As(outer, &coded) {
		t.Fatalf("errors.As found no code-reporting error in the chain: %v", outer)
	}
	if coded.ErrorShortCode() != shortCyclicExtension {
		t.Errorf("ErrorShortCode() = %q; want %q", coded.ErrorShortCode(), shortCyclicExtension)
	}

	// Both identifiers resolve to the same condition off the same chain.
	if code, _ := cerrors.CodeOf(outer); code != codeCyclicExtension {
		t.Errorf("the two reads disagree about which condition the chain names: CodeOf = %q, "+
			"ShortCodeOf = %q; want %q and %q", code, got, codeCyclicExtension, shortCyclicExtension)
	}

	// Two identities sharing an exported accessor name in two packages carry two
	// short codes, and the read must not collapse them.
	if got, _ := cerrors.ShortCodeOf(fmt.Errorf("merging: %w", errSiblingCyclicExtension())); got != shortSiblingCyclicExtension {
		t.Errorf("ShortCodeOf = %q; want the merger identity's own short code %q", got, shortSiblingCyclicExtension)
	}

	// A chain carrying no registered identity reports no short code — the same
	// fact CodeOf and MessageOf report about the same chain.
	if got, ok := cerrors.ShortCodeOf(stderrors.New("an ordinary error")); ok {
		t.Errorf("ShortCodeOf reported %q for a chain carrying no identity", got)
	}
	if _, ok := cerrors.ShortCodeOf(nil); ok {
		t.Errorf("ShortCodeOf reported a short code for a nil error")
	}
}

// TestIdentity_ForgingTheShortCodeStringYieldsNoIdentity extends the asymmetry
// the split exists for to the second identifier. Neither identifier is an input
// to matching, and the short code is the more tempting of the two to write back
// because it is short enough to type.
func TestIdentity_ForgingTheShortCodeStringYieldsNoIdentity(t *testing.T) {
	t.Parallel()

	forged := stderrors.New(shortCyclicExtension)
	if stderrors.Is(forged, errCyclicExtension()) {
		t.Errorf("an error built from the short code string matched the identity, so the short code is an input to matching")
	}
	if stderrors.Is(errCyclicExtension(), forged) {
		t.Errorf("the identity matched an error built from its own short code string")
	}
}

// TestRegister_IsSafeUnderConcurrentUse establishes rather than assumes that
// the registry needs no external synchronisation. Registration is expected at
// package initialisation, which is single-goroutine, but that is a convention
// of how the call is used and not a property the exported function can rely on.
// Under the race detector this fails if the guard is ever removed.
func TestRegister_IsSafeUnderConcurrentUse(t *testing.T) {
	t.Parallel()

	const registrants = 16
	var wg sync.WaitGroup
	got := make([]error, registrants)

	for i := range registrants {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			got[n] = cerrors.Register(
				fmt.Sprintf("concurrency/registrant-%02d", n),
				cerrors.Meta{
					// Both identifiers vary with n, because both are indexed:
					// a fixed short code across the registrants would be
					// refused as a duplicate rather than exercising the guard.
					ShortCode: fmt.Sprintf("E93%02d", n),
					Message:   fmt.Sprintf("registrant %02d", n),
				},
			)
		}(i)

		wg.Add(1)
		go func() {
			defer wg.Done()
			// Reads run against the registry while the writes above are in flight.
			_ = errCyclicExtension().Error()
			_ = errSiblingCyclicExtension().Error()
		}()
	}
	wg.Wait()

	for i := range registrants {
		want := fmt.Sprintf("registrant %02d", i)
		if got[i].Error() != want {
			t.Errorf("identity %d rendered %q; want %q", i, got[i].Error(), want)
		}
		code, ok := cerrors.CodeOf(got[i])
		if !ok || code != fmt.Sprintf("concurrency/registrant-%02d", i) {
			t.Errorf("identity %d reported code %q (found=%v); want %q",
				i, code, ok, fmt.Sprintf("concurrency/registrant-%02d", i))
		}
		// The short-code index is written under the same lock and is checked
		// here for the same reason: an unsynchronised second map would corrupt
		// or lose entries exactly as the first one would.
		short, ok := cerrors.ShortCodeOf(got[i])
		if !ok || short != fmt.Sprintf("E93%02d", i) {
			t.Errorf("identity %d reported short code %q (found=%v); want %q",
				i, short, ok, fmt.Sprintf("E93%02d", i))
		}
	}
}

// panicValue runs fn and returns the value it panicked with, or nil when fn
// returned normally.
func panicValue(fn func()) (v any) {
	defer func() { v = recover() }()
	fn()
	return nil
}

// TestRegister_PanicsOnABlankDeclaredAheadNote pins the property that makes the
// note worth having: it cannot be acquired by neglect.
//
// A condition nothing raises reaches the published reference only by naming the
// work that will raise it. A note carrying no text would satisfy that door while
// telling a reader nothing — it would take the condition off the unraised report
// AND put it on the page with nothing beside it, which is precisely the
// suppression the field must not become. So a present-but-blank value is refused
// where an absent one is accepted.
//
// The probe uses whitespace rather than the empty string, because the empty
// string is the ordinary absent value and refusing it would refuse nearly every
// registration in the module.
func TestRegister_PanicsOnABlankDeclaredAheadNote(t *testing.T) {
	t.Parallel()

	v := panicValue(func() {
		cerrors.Register(codeBlankDeclaredAhead, cerrors.Meta{
			ShortCode:     shortBlankDeclaredAhead,
			Message:       "a message whose declared-ahead note says nothing",
			DeclaredAhead: "   \t  ",
		})
	})
	if v == nil {
		t.Fatalf("registering a blank declared-ahead note did not panic")
	}
	if msg := fmt.Sprint(v); !strings.Contains(msg, codeBlankDeclaredAhead) {
		t.Errorf("panic %q does not name the offending code %q", msg, codeBlankDeclaredAhead)
	}

	// The refused registration must not have landed in either index, for the
	// reason the empty-message refusal checks the same thing: a half-written
	// entry would make the repaired declaration fail as a duplicate.
	if v := panicValue(func() {
		cerrors.Register(codeBlankDeclaredAhead, cerrors.Meta{
			ShortCode:     shortBlankDeclaredAhead,
			Message:       "a message whose declared-ahead note says nothing",
			DeclaredAhead: "It will report the condition once the work that raises it lands.",
		})
	}); v != nil {
		t.Errorf("a refused registration left one of the two indexes occupied; re-registering panicked with %v", v)
	}
}

// TestRegister_AcceptsAnAbsentAndAWrittenDeclaredAheadNote is the calibration for
// the refusal above. Absent is the ordinary value and carries no note at all; a
// written one is the deliberate exception. A refusal that also caught either of
// these would refuse the whole module at package initialisation, so the negative
// case is asserted rather than assumed.
func TestRegister_AcceptsAnAbsentAndAWrittenDeclaredAheadNote(t *testing.T) {
	t.Parallel()

	if v := panicValue(func() {
		cerrors.Register(codeSetDeclaredAhead, cerrors.Meta{
			ShortCode:     shortSetDeclaredAhead,
			Message:       "a message whose declared-ahead note names the work",
			DeclaredAhead: "It will report the condition once the work that raises it lands.",
		})
	}); v != nil {
		t.Errorf("a written declared-ahead note was refused: %v", v)
	}

	// The absent case is exercised by every other registration in this file and
	// in the module, so asserting it again here would restate what package
	// initialisation already proves.
}

// TestRegister_PanicsOnARulePlaceWhoseReasonSaysNothing pins the half of the
// place declaration the constructors cannot pin.
//
// A place that is a departure from a rule over authored input takes its reason
// as a constructor argument, so a declaration that omits it does not compile at
// all — the refusal below is unreachable through the missing-argument route and
// nothing here probes it, because a compile error is not a runtime value to
// assert against. What is left is the argument that is present and says nothing,
// which compiles and would move a condition out of the count of rules not yet
// expressed as the dispatch's own while naming no reason for the move. That is
// the exemption the field must not become, so it is refused.
//
// Both of the two places that take a reason are probed. They are separate arms
// of one predicate, and an assertion over one of them would pass over a
// predicate that had stopped reading the other.
func TestRegister_PanicsOnARulePlaceWhoseReasonSaysNothing(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		place cerrors.RulePlace
	}{
		{"not a rule at all", cerrors.NotARule("   \t  ")},
		{"a rule another evaluator owns", cerrors.RuleEvaluatedElsewhere("  ")},
	} {
		v := panicValue(func() {
			cerrors.Register(codeBlankRulePlaceReason, cerrors.Meta{
				ShortCode: shortBlankRulePlaceReason,
				Message:   "a message whose rule place carries no reason",
				RulePlace: tc.place,
			})
		})
		if v == nil {
			t.Fatalf("registering %s with a blank reason did not panic", tc.name)
		}
		if msg := fmt.Sprint(v); !strings.Contains(msg, codeBlankRulePlaceReason) {
			t.Errorf("panic %q for %s does not name the offending code %q", msg, tc.name, codeBlankRulePlaceReason)
		}
	}

	// The refused registrations must not have landed in either index, for the
	// reason every refusal above checks the same thing: a half-written entry
	// would make the repaired declaration fail as a duplicate.
	if v := panicValue(func() {
		cerrors.Register(codeBlankRulePlaceReason, cerrors.Meta{
			ShortCode: shortBlankRulePlaceReason,
			Message:   "a message whose rule place carries no reason",
			RulePlace: cerrors.NotARule("A registry answered the request; nothing anybody authored decides it."),
		})
	}); v != nil {
		t.Errorf("a refused registration left one of the two indexes occupied; re-registering panicked with %v", v)
	}
}

// TestRegister_AcceptsEachOfTheThreePlaces is the calibration for the refusal
// above, and it is owed for the reason the other calibrations here are:
// a refusal that also caught a well-formed declaration would refuse the module
// at package initialisation the moment the declared set is written.
//
// All three places are exercised rather than one. They differ in whether a
// reason is carried at all, so a predicate that had come to refuse the ordinary
// place — the one carrying none — would pass an assertion taken over either of
// the other two.
//
// The absent case is not asserted here: every other registration in this file
// and in the module carries no place, so package initialisation proves it.
func TestRegister_AcceptsEachOfTheThreePlaces(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		code      string
		shortCode string
		place     cerrors.RulePlace
	}{
		{
			name:      "a rule over authored input",
			code:      codePlaceOverAuthoredInput,
			shortCode: shortPlaceOverAuthoredInput,
			place:     cerrors.RuleOverAuthoredInput(),
		},
		{
			name:      "a rule another evaluator owns",
			code:      codePlaceEvaluatedElsewhere,
			shortCode: shortPlaceEvaluatedElsewhere,
			place:     cerrors.RuleEvaluatedElsewhere("A plugin guest evaluates the rule, so no dispatch invokes it."),
		},
		{
			name:      "not a rule at all",
			code:      codePlaceNotARule,
			shortCode: shortPlaceNotARule,
			place:     cerrors.NotARule("A registry answered the request; nothing anybody authored decides it."),
		},
	} {
		if v := panicValue(func() {
			cerrors.Register(tc.code, cerrors.Meta{
				ShortCode: tc.shortCode,
				Message:   "a message declaring " + tc.name,
				RulePlace: tc.place,
			})
		}); v != nil {
			t.Errorf("a well-formed declaration of %s was refused: %v", tc.name, v)
		}
	}
}
