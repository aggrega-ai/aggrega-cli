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
	codeCyclicExtension       = "validator/cyclic-extension"
	codeMergerCyclicExtension = "validator-merger/cyclic-extension"
	codeDuplicate             = "resolver/duplicate-subject"
	codeEmptyMessage          = "resolver/empty-message-subject"
)

// The two messages below are the real, distinct texts carried today by two
// independently declared rules that share one exported name in two different
// packages. They are reproduced verbatim because the property under test is
// precisely that such a pair stays two identities with two messages.
const (
	messageCyclicExtension       = "cyclic extends chain"
	messageMergerCyclicExtension = "merger: cyclic extends chain"
)

var (
	identityCyclicExtension       = cerrors.Register(codeCyclicExtension, cerrors.Meta{Message: messageCyclicExtension})
	identityMergerCyclicExtension = cerrors.Register(codeMergerCyclicExtension, cerrors.Meta{Message: messageMergerCyclicExtension})
	identityDuplicate             = cerrors.Register(codeDuplicate, cerrors.Meta{Message: "the first registration of this code"})
)

func errCyclicExtension() error       { return identityCyclicExtension }
func errMergerCyclicExtension() error { return identityMergerCyclicExtension }

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
	if stderrors.Is(outer, errMergerCyclicExtension()) {
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
	if got, _ := cerrors.MessageOf(fmt.Errorf("merging: %w", errMergerCyclicExtension())); got != messageMergerCyclicExtension {
		t.Errorf("MessageOf = %q; want the merger identity's own sentence %q", got, messageMergerCyclicExtension)
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

// TestIdentity_SameRuleNameInTwoPackagesStaysTwoIdentities is the reason a code
// value names its declaring package as well as its rule. Two rules sharing one
// exported name in two packages are distinct conditions today; a value derived
// from the rule name alone would merge them, so errors.Is would begin matching
// across the pair and only one of the two messages could be registered.
func TestIdentity_SameRuleNameInTwoPackagesStaysTwoIdentities(t *testing.T) {
	t.Parallel()

	if errCyclicExtension() == errMergerCyclicExtension() {
		t.Fatalf("the two accessors yield one identity, so they are one condition")
	}
	if got := errCyclicExtension().Error(); got != messageCyclicExtension {
		t.Errorf("first identity Error() = %q; want %q", got, messageCyclicExtension)
	}
	if got := errMergerCyclicExtension().Error(); got != messageMergerCyclicExtension {
		t.Errorf("second identity Error() = %q; want %q", got, messageMergerCyclicExtension)
	}

	wrapped := fmt.Errorf("resolving the aggregate: %w", errCyclicExtension())
	if stderrors.Is(wrapped, errMergerCyclicExtension()) {
		t.Errorf("errors.Is matched across two distinct identities")
	}
}

func TestRegister_PanicsOnADuplicateCode(t *testing.T) {
	t.Parallel()

	v := panicValue(func() {
		cerrors.Register(codeDuplicate, cerrors.Meta{Message: "a second registration of the same code"})
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
		cerrors.Register("", cerrors.Meta{Message: "a message for no identity"})
	})
	if v == nil {
		t.Fatalf("registering an empty code did not panic")
	}
}

func TestRegister_PanicsOnAnEmptyMessage(t *testing.T) {
	t.Parallel()

	v := panicValue(func() {
		cerrors.Register(codeEmptyMessage, cerrors.Meta{})
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
	if v := panicValue(func() {
		cerrors.Register(codeEmptyMessage, cerrors.Meta{Message: "a message this time"})
	}); v != nil {
		t.Errorf("a refused registration left the code occupied; re-registering panicked with %v", v)
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
				cerrors.Meta{Message: fmt.Sprintf("registrant %02d", n)},
			)
		}(i)

		wg.Add(1)
		go func() {
			defer wg.Done()
			// Reads run against the registry while the writes above are in flight.
			_ = errCyclicExtension().Error()
			_ = errMergerCyclicExtension().Error()
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
	}
}

// panicValue runs fn and returns the value it panicked with, or nil when fn
// returned normally.
func panicValue(fn func()) (v any) {
	defer func() { v = recover() }()
	fn()
	return nil
}
