// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
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
//
// It also pins which chain the diagnostic's OWN head is composed from, because
// the two are easy to confuse once both carry a code. The head follows the lead:
// the decoration decides which of the two failures the reader meets first, and
// the head names that one's condition. No refusal is singled out here — the
// failure carries no unresolved-reference frame to match one against — so the
// decorated failure leads and its own code heads the diagnostic, while the
// refused file's code stays where the refused file is reported.
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
	// The refused file's code must NOT head the diagnostic. Heading with it
	// would claim a cause the record could not establish — no refusal was
	// singled out — and a reader acts on the code at the top of the line.
	first := strings.SplitN(rendered, "\n", 2)[0]
	if strings.Contains(first, wantCode) {
		t.Errorf("no refusal was singled out, so the refused file's code %s must not head the diagnostic; head was:\n%s", wantCode, first)
	}
	// And the head carries the leading failure's own code, which is what makes
	// the line quotable and lookupable at all.
	leadCode, ok := cerrors.ShortCodeOf(catalog.ErrUnscopedRecipeReference())
	if !ok {
		t.Fatal("the resolution condition must be registered with a short code")
	}
	if !strings.Contains(first, leadCode) {
		t.Errorf("the head must carry the leading failure's own short code %s; head was:\n%s", leadCode, first)
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

// TestWithSkipContext_FormatterCarriesAFrameAboveTheDecoration guards the one
// thing recursing on the decorated failure alone would drop: text a frame ABOVE
// the decoration contributed. A step label is what tells the reader which step
// failed, so the formatter carries it across rather than rendering the decorated
// failure on its own.
//
// WHAT THIS PROVES AND WHAT IT DOES NOT, stated because the two are easy to
// confuse. It builds its own wrapping frame, so its whole subject is the
// FORMATTER: given a frame above a decoration, that frame reaches the reader.
// It says nothing about whether any production path puts a frame there, and it
// keeps passing with every such frame deleted. The guard for the frame the
// in-build fill actually contributes is
// TestBuildAutoPull_StepLabelReachesTheReader below, which constructs no wrap
// and drives the command instead.
func TestWithSkipContext_FormatterCarriesAFrameAboveTheDecoration(t *testing.T) {
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

// TestBuildAutoPull_StepLabelReachesTheReader drives a build whose fill fails
// and asserts the step label reaches the terminal. It constructs no error and no
// wrap: the fixture refuses a Recipe the Release's closure reaches, the fill's
// resolution fails on it, and every frame in the rendered diagnostic is one the
// product put there.
//
// That is the whole point of it. A reader meeting a build that stopped needs to
// know WHICH step stopped it, and the only thing carrying that here is the frame
// the fill contributes. A guard that supplies that frame itself would keep
// passing with the product's own frame deleted, so this one takes the frame from
// the product or fails.
//
// It also asserts the decoration is what the formatter rendered, because the
// label survives on that branch and is deliberately discarded on the envelope
// branch. Without it a future change that routes this failure to a different
// branch would take the label with it, and a prefix assertion alone cannot tell
// the two apart.
func TestBuildAutoPull_StepLabelReachesTheReader(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath, catalogRoot := writeValidWorkspace(t, root)
	// The Release's closure is exactly one Component, one Recipe and one
	// Provider, so refusing that Recipe is what makes the fill's resolution fail
	// with a refusal the load recorded — the shape the pull's own annotator
	// decorates.
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "helm", "server.yaml"),
		refusedRecipe("server-recipe", "1.0.0", "helm"))

	lockfilePath := filepath.Join(root, "release.lock")
	if _, err := os.Stat(lockfilePath); !os.IsNotExist(err) {
		t.Fatalf("precondition: the fill must be the step that runs, so there must be no lockfile yet; stat err = %v", err)
	}

	var diagBuf, outBuf bytes.Buffer
	err := dispatchBuildWithDiag(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		releasePath, []string{catalogRoot}, false, buildDiagOptions{},
		buildops.RunOptions{AutoPull: true}, &diagBuf, &outBuf)
	if err == nil {
		t.Fatal("the fill must fail on a refused in-closure Recipe; there is no diagnostic to read")
	}
	rendered := formatCompilerError(err)

	if !strings.HasPrefix(rendered, "auto-pull: ") {
		t.Errorf("the step label the fill contributes must reach the terminal; rendered:\n%s", rendered)
	}
	if !strings.Contains(rendered, "skipped at load") {
		t.Errorf("the failure must reach the reader through the decoration, which is the branch that carries the label; rendered:\n%s", rendered)
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
