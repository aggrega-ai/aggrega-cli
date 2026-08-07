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

// The identities below are declared and registered exactly as a consuming
// package writes them: a constant for the identity, and a package-level
// registration standing beside it. Declaring them here means the registrations
// run at package initialisation, which is the shape being tested rather than a
// convenience of the test.
//
// Every code value in this file is distinct. The registry is process-wide, so a
// repeated value would be a duplicate registration and would panic at init.
const (
	codeCyclicExtension       cerrors.Code = "validator/cyclic-extension"
	codeMergerCyclicExtension cerrors.Code = "validator-merger/cyclic-extension"
	codeUnregistered          cerrors.Code = "renderer/never-registered"
	codeDuplicate             cerrors.Code = "resolver/duplicate-subject"
	codeEmptyMessage          cerrors.Code = "resolver/empty-message-subject"
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
	_ = cerrors.Register(codeCyclicExtension, cerrors.Meta{Message: messageCyclicExtension})
	_ = cerrors.Register(codeMergerCyclicExtension, cerrors.Meta{Message: messageMergerCyclicExtension})
	_ = cerrors.Register(codeDuplicate, cerrors.Meta{Message: "the first registration of this code"})
)

// A constant of the identity type satisfies error. This assertion is the
// compile-time half of the guarantee: it fails to build rather than to run if
// the value receiver is ever changed to a pointer receiver, which would leave
// only addressable values implementing the interface and so exclude constants.
var _ error = codeCyclicExtension

func TestCode_ErrorReturnsTheRegisteredMessageByteForByte(t *testing.T) {
	t.Parallel()
	if got := codeCyclicExtension.Error(); got != messageCyclicExtension {
		t.Errorf("Error() = %q; want %q", got, messageCyclicExtension)
	}
}

// TestCode_IsMatchesThroughTwoWrapLayers is the instrument for the property a
// caller depends on: an identity stays matchable however deeply the error is
// wrapped on its way up. Two layers rather than one, because a single layer
// would also pass for a comparison that only ever inspects the outermost error.
func TestCode_IsMatchesThroughTwoWrapLayers(t *testing.T) {
	t.Parallel()

	inner := fmt.Errorf("reading the extends chain: %w", codeCyclicExtension)
	outer := fmt.Errorf("resolving the aggregate: %w", inner)

	if !stderrors.Is(outer, codeCyclicExtension) {
		t.Errorf("errors.Is failed to match the identity through two wrap layers; chain: %v", outer)
	}
	if stderrors.Is(outer, codeMergerCyclicExtension) {
		t.Errorf("errors.Is matched a different identity through the same chain; chain: %v", outer)
	}

	// A third layer, to establish that the match is depth-independent rather
	// than passing at exactly the depth the test happens to build.
	deepest := fmt.Errorf("building the graph: %w", outer)
	if !stderrors.Is(deepest, codeCyclicExtension) {
		t.Errorf("errors.Is failed to match the identity through three wrap layers; chain: %v", deepest)
	}
}

// TestCode_RegisteredMessageSurvivesWrapping pins the property that makes the
// registered message the user-visible text: every enclosing frame renders it
// unchanged, so moving a message into a registration moves no output.
func TestCode_RegisteredMessageSurvivesWrapping(t *testing.T) {
	t.Parallel()

	inner := fmt.Errorf("reading the extends chain: %w", codeCyclicExtension)
	outer := fmt.Errorf("resolving the aggregate: %w", inner)

	want := "resolving the aggregate: reading the extends chain: " + messageCyclicExtension
	if got := outer.Error(); got != want {
		t.Errorf("wrapped text = %q; want %q", got, want)
	}
}

// TestCode_SameRuleNameInTwoPackagesStaysTwoIdentities is the reason a code
// value names its declaring package as well as its rule. Two rules sharing one
// exported name in two packages are distinct conditions today; a value derived
// from the rule name alone would merge them, so errors.Is would begin matching
// across the pair and only one of the two messages could be registered.
func TestCode_SameRuleNameInTwoPackagesStaysTwoIdentities(t *testing.T) {
	t.Parallel()

	if codeCyclicExtension == codeMergerCyclicExtension {
		t.Fatalf("the two identities share one value, so they are one identity: %q", codeCyclicExtension)
	}
	if got := codeCyclicExtension.Error(); got != messageCyclicExtension {
		t.Errorf("first identity Error() = %q; want %q", got, messageCyclicExtension)
	}
	if got := codeMergerCyclicExtension.Error(); got != messageMergerCyclicExtension {
		t.Errorf("second identity Error() = %q; want %q", got, messageMergerCyclicExtension)
	}

	wrapped := fmt.Errorf("resolving the aggregate: %w", codeCyclicExtension)
	if stderrors.Is(wrapped, codeMergerCyclicExtension) {
		t.Errorf("errors.Is matched across two distinct identities")
	}
}

// TestCode_UnregisteredIsLoudAndNeverEmpty pins the failure shape for an
// identity declared without a registration. The empty string is the one
// forbidden answer: inside an enclosing wrap it renders as a dangling
// separator, which reads as a truncated diagnostic rather than as a defect.
func TestCode_UnregisteredIsLoudAndNeverEmpty(t *testing.T) {
	t.Parallel()

	got := codeUnregistered.Error()
	if got == "" {
		t.Fatalf("an unregistered code returned the empty string")
	}
	if !strings.Contains(got, string(codeUnregistered)) {
		t.Errorf("unregistered text %q does not name the code %q, so the missing registration cannot be found",
			got, string(codeUnregistered))
	}

	// Through a wrap, the enclosing frame must still be followed by text.
	wrapped := fmt.Errorf("resolving the aggregate: %w", codeUnregistered).Error()
	if strings.HasSuffix(wrapped, ": ") {
		t.Errorf("wrapped text ends in a dangling separator: %q", wrapped)
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
	if msg := fmt.Sprint(v); !strings.Contains(msg, string(codeDuplicate)) {
		t.Errorf("panic %q does not name the duplicated code %q", msg, string(codeDuplicate))
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
	if msg := fmt.Sprint(v); !strings.Contains(msg, string(codeEmptyMessage)) {
		t.Errorf("panic %q does not name the offending code %q", msg, string(codeEmptyMessage))
	}
	// The refused registration must not have landed: a code rejected at the
	// door is unregistered, not registered with empty text.
	if got := codeEmptyMessage.Error(); got == "" {
		t.Errorf("a refused registration left the code returning the empty string")
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

	for i := range registrants {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			code := cerrors.Code(fmt.Sprintf("concurrency/registrant-%02d", n))
			cerrors.Register(code, cerrors.Meta{Message: fmt.Sprintf("registrant %02d", n)})
		}(i)

		wg.Add(1)
		go func() {
			defer wg.Done()
			// Reads run against the map while the writes above are in flight.
			_ = codeCyclicExtension.Error()
			_ = codeUnregistered.Error()
		}()
	}
	wg.Wait()

	for i := range registrants {
		code := cerrors.Code(fmt.Sprintf("concurrency/registrant-%02d", i))
		want := fmt.Sprintf("registrant %02d", i)
		if got := code.Error(); got != want {
			t.Errorf("Error() for %q = %q; want %q", string(code), got, want)
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
