// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	stderrors "errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// refusedRecipeSkip is one refused catalog file in the shape the tolerant load
// records it: the header re-parse recovered the kind and name, and the reason is
// the envelope the document's own kind schema raised — a registered condition,
// so it carries a short code.
func refusedRecipeSkip() catalog.SkippedEntity {
	return catalog.SkippedEntity{
		Path: "/cat/recipes/helm/server.yaml",
		Kind: "Recipe",
		Name: "server-recipe",
		Err: cerrors.Wrap(
			envelope.ErrEntityEnvelope(),
			cerrors.CatalogError,
			cerrors.SourceLocation{File: "/cat/recipes/helm/server.yaml", Line: 6, Column: 1},
			"spec.implements: missing required property: implements",
		),
	}
}

// TestWithSkipContext_SentinelSurvivesToTheReader is acceptance clause 3 read
// against the code path that formats the final message. The decorated failure's
// registered condition has to stay matchable at the command boundary — both by
// errors.Is, and by the short-code lookup the formatter's head uses — because a
// chain rendered into text loses both while still reading correctly.
func TestWithSkipContext_SentinelSurvivesToTheReader(t *testing.T) {
	failure := fmt.Errorf("resolve: resolve entity %q: %w", "server-recipe:1.0.0", catalog.ErrUnscopedRecipeReference())
	got := withSkipContext(failure, []catalog.SkippedEntity{refusedRecipeSkip()})

	if !stderrors.Is(got, catalog.ErrUnscopedRecipeReference()) {
		t.Errorf("errors.Is must still match the decorated failure's condition; got %v", got)
	}
	if code, ok := cerrors.ShortCodeOf(got); !ok || code != "E0105" {
		t.Errorf("ShortCodeOf(decorated) = %q, %v; want the decorated failure's own short code", code, ok)
	}
	if unwrapped := stderrors.Unwrap(got); unwrapped != failure {
		t.Errorf("Unwrap must return the decorated failure itself; got %v", unwrapped)
	}
}

// TestWithSkipContext_NoteReachesTheTerminalWhateverTheChainShape plants the two
// chain shapes that reach withSkipContext and asserts the note survives the
// formatter in both.
//
// The second shape is the one that matters and the one a naive implementation
// fails: the formatter renders a diagnostic from the outermost categorised
// envelope it can reach and discards every frame above it, so a note appended by
// an fmt wrap disappears the moment the decorated failure carries an envelope —
// on exactly the path where the note is the only text naming what the author
// wrote wrong. The assertion exists to fail on that implementation.
func TestWithSkipContext_NoteReachesTheTerminalWhateverTheChainShape(t *testing.T) {
	skips := []catalog.SkippedEntity{refusedRecipeSkip()}

	for _, tc := range []struct {
		name    string
		failure error
	}{
		{
			name:    "no envelope in the chain",
			failure: fmt.Errorf("resolve: resolve entity %q: %w", "server-recipe:1.0.0", catalog.ErrUnscopedRecipeReference()),
		},
		{
			name: "an envelope in the chain",
			failure: cerrors.Wrap(fmt.Errorf("boom"), cerrors.CatalogError,
				cerrors.SourceLocation{File: "/cat/base/components/server.yaml"}, "unmarshal Component"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rendered := formatCompilerError(withSkipContext(tc.failure, skips))
			if !strings.Contains(rendered, "catalog files skipped at load") {
				t.Errorf("the skip note must reach the terminal; rendered:\n%s", rendered)
			}
			if !strings.Contains(rendered, `Recipe "server-recipe" (/cat/recipes/helm/server.yaml)`) {
				t.Errorf("the refused file's identity must reach the terminal; rendered:\n%s", rendered)
			}
			if !strings.Contains(rendered, "spec.implements: missing required property") {
				t.Errorf("the refusal's own reason must reach the terminal; rendered:\n%s", rendered)
			}
		})
	}
}

// TestWithSkipContext_RefusedFileCarriesItsShortCode is acceptance clause 1 at the
// unit boundary: what the reader sees for the refused file names the condition by
// its registered short code, in the bracketed head beside the category — the one
// position the error reference teaches a code appears in.
func TestWithSkipContext_RefusedFileCarriesItsShortCode(t *testing.T) {
	failure := fmt.Errorf("resolve: resolve entity %q: %w", "server-recipe:1.0.0", catalog.ErrUnscopedRecipeReference())
	rendered := formatCompilerError(withSkipContext(failure, []catalog.SkippedEntity{refusedRecipeSkip()}))

	wantCode, ok := cerrors.ShortCodeOf(envelope.ErrEntityEnvelope())
	if !ok {
		t.Fatal("the envelope-validation condition must be registered with a short code")
	}
	wantHead := "[" + string(cerrors.CatalogError) + " " + wantCode + "]"
	if !strings.Contains(rendered, wantHead) {
		t.Errorf("the refused file's line must carry %s; rendered:\n%s", wantHead, rendered)
	}
	// The consequence's code must NOT be promoted to the head: a code is a
	// promise that it names the thing that went wrong, and the registry's
	// provider-scoping condition is not what the author wrote wrong.
	consequence, _ := cerrors.ShortCodeOf(catalog.ErrUnscopedRecipeReference())
	if first := strings.SplitN(rendered, "\n", 2)[0]; strings.Contains(first, consequence) {
		t.Errorf("the head must not carry the consequence's short code %s; head was:\n%s", consequence, first)
	}
}

// TestWithSkipContext_NoteAssertsTheClosureMembershipDistinction is acceptance
// clause 4: the reporting site, not only the loader, says which class of refusal
// can explain the failure the note is attached to. The note is attached to every
// refusal the load recorded rather than only to the one that mattered, so a
// reader who is not told which class is which cannot tell a cause from a
// bystander.
func TestWithSkipContext_NoteAssertsTheClosureMembershipDistinction(t *testing.T) {
	failure := fmt.Errorf("resolve: resolve entity %q: %w", "server-recipe:1.0.0", catalog.ErrUnscopedRecipeReference())
	rendered := formatCompilerError(withSkipContext(failure, []catalog.SkippedEntity{refusedRecipeSkip()}))

	for _, want := range []string{
		"outside this Release's closure is tolerated and never fails a build",
		"explained only by a refusal naming something this Release reaches",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the note must state %q; rendered:\n%s", want, rendered)
		}
	}
}

// TestWithSkipContext_OuterLabelSurvives guards the one thing recursing on the
// decorated failure alone would drop: text a frame ABOVE the decoration
// contributed. The in-build pull wraps its failure with the step that was
// running, and that label is what tells the reader which step failed.
func TestWithSkipContext_OuterLabelSurvives(t *testing.T) {
	failure := cerrors.Wrap(fmt.Errorf("boom"), cerrors.CatalogError,
		cerrors.SourceLocation{File: "/cat/base/components/server.yaml"}, "unmarshal Component")
	decorated := withSkipContext(failure, []catalog.SkippedEntity{refusedRecipeSkip()})
	rendered := formatCompilerError(fmt.Errorf("auto-pull: %w", decorated))

	if !strings.HasPrefix(rendered, "auto-pull: ") {
		t.Errorf("a label above the decoration must survive to the terminal; rendered:\n%s", rendered)
	}
	if !strings.Contains(rendered, "catalog files skipped at load") {
		t.Errorf("the note must survive a wrap above the decoration; rendered:\n%s", rendered)
	}
}

// TestShortCodeInHead_IsIdempotentAndDeclines covers the two shapes the head
// injection must refuse rather than mangle.
func TestShortCodeInHead_IsIdempotentAndDeclines(t *testing.T) {
	if got := shortCodeInHead("[CATALOG_ERROR] x: y", "E0215"); got != "[CATALOG_ERROR E0215] x: y" {
		t.Errorf("injection into a one-token head = %q", got)
	}
	if got := shortCodeInHead("[CATALOG_ERROR E0215] x: y", "E0001"); got != "[CATALOG_ERROR E0215] x: y" {
		t.Errorf("a head already carrying a code must be left alone; got %q", got)
	}
	if got := shortCodeInHead("plain reason", "E0215"); got != "plain reason" {
		t.Errorf("a reason with no bracketed head must be left alone; got %q", got)
	}
}
