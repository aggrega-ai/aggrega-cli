// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package errors_test

import (
	stderrors "errors"
	"fmt"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// The identity this file registers, declared the way a consuming package writes
// one. Its short code sits in the high band the other test files use, clear of
// their runs; it is not an allocation and must not be read off the published
// mapping.
var identityOccurrenceSubject = cerrors.Register("occurrence-subject",
	cerrors.Meta{ShortCode: "E9231", Message: "the occurrence subject was reached"})

// TestOccurrence_CarriesItsConditionBesideTheComposedText reads an occurrence the
// ways a surface reads a finding: matched against its condition, its two codes
// read off the chain, and the chain flattened onto one line.
//
// The flattened line is the property the constructor exists for. It must be the
// composed text and nothing more — the condition's registered sentence must not
// follow it. Adding an Unwrap to the occurrence makes the first two rows fail
// here, because the flattening walk would then reach the condition and append its
// sentence; that is the plant that shows this test can fail.
func TestOccurrence_CarriesItsConditionBesideTheComposedText(t *testing.T) {
	t.Parallel()
	const text = `binding for feature "core/x" maps multiple subkey paths ([a b]); the value cannot route`
	occurrence := cerrors.Occurrence(identityOccurrenceSubject, text)

	for _, row := range []struct {
		name      string
		err       error
		flattened string
	}{
		{
			name:      "the occurrence on its own",
			err:       occurrence,
			flattened: text,
		},
		{
			name:      "the occurrence under a wrap adding context",
			err:       fmt.Errorf("annotation parse failed at key %q: %w", "k", occurrence),
			flattened: `annotation parse failed at key "k": ` + text,
		},
		{
			name: "the occurrence joined beneath a categorised envelope",
			err: cerrors.Wrap(stderrors.Join(occurrence), cerrors.OpsError, cerrors.SourceLocation{},
				"annotation expansion failed"),
			flattened: "[OPS_ERROR] annotation expansion failed: " + text,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			if !stderrors.Is(row.err, identityOccurrenceSubject) {
				t.Errorf("errors.Is against the condition = false, want true; got %v", row.err)
			}
			if got, ok := cerrors.CodeOf(row.err); !ok || got != "occurrence-subject" {
				t.Errorf("CodeOf = %q (found=%v), want %q", got, ok, "occurrence-subject")
			}
			if got, ok := cerrors.ShortCodeOf(row.err); !ok || got != "E9231" {
				t.Errorf("ShortCodeOf = %q (found=%v), want %q", got, ok, "E9231")
			}
			if got := cerrors.CauseChain(row.err); got != row.flattened {
				t.Errorf("flattened chain\n got: %s\nwant: %s", got, row.flattened)
			}
		})
	}

	if got := occurrence.Error(); got != text {
		t.Errorf("Error\n got: %s\nwant: %s", got, text)
	}
	if got, ok := cerrors.MessageOf(occurrence); !ok || got != "the occurrence subject was reached" {
		t.Errorf("MessageOf = %q (found=%v), want the condition's registered sentence", got, ok)
	}
	if stderrors.Is(occurrence, identityEmbeddedSubject) {
		t.Error("the occurrence matched a condition it is not an occurrence of")
	}
}

// TestOccurrence_AdmitsOnlyARegisteredIdentity holds the constructor to the
// refusals Raise makes: a condition no Register call produced is refused, and a
// nil one says so in its own words. The admitted row keeps a constructor that
// refused everything from passing the rest of the table.
func TestOccurrence_AdmitsOnlyARegisteredIdentity(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name      string
		condition cerrors.Identity
		wantPanic string
	}{
		{name: "the identity a Register call returned is admitted", condition: identityOccurrenceSubject},
		{name: "a struct embedding the identity interface is refused", condition: embeddedForgery{},
			wantPanic: "no Register call produced"},
		{name: "embedding a registered identity is refused",
			condition: embeddedForgery{Identity: identityOccurrenceSubject}, wantPanic: "no Register call produced"},
		{name: "a nil condition is refused", condition: nil, wantPanic: "nil condition"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			recovered := occurrenceRecovering(c.condition)
			if c.wantPanic == "" {
				if recovered != nil {
					t.Fatalf("the constructor refused an identity Register produced: %v", recovered)
				}
				return
			}
			if recovered == nil {
				t.Fatal("the constructor admitted a condition no Register call produced")
			}
			if got := fmt.Sprint(recovered); !strings.Contains(got, c.wantPanic) ||
				!strings.Contains(got, "Occurrence") {
				t.Errorf("the refusal does not name this constructor and this fault\nwant fragments %q and %q\ngot: %s",
					"Occurrence", c.wantPanic, got)
			}
		})
	}
}

// occurrenceRecovering calls the constructor and reports what it panicked with.
func occurrenceRecovering(condition cerrors.Identity) (recovered any) {
	defer func() { recovered = recover() }()
	_ = cerrors.Occurrence(condition, "the text")
	return nil
}
