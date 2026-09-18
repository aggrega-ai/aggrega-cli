// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

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

// TestAnnotateSkipContext_LeadingPlusNoteIsTheWholeText pins the relationship
// between the two accessors and the one-string form, because two front ends read
// the decoration two ways: one renders Leading through its own formatter and
// appends Note, the other takes Error whole. A split that did not reproduce the
// whole would leave those two telling a reader different things about the same
// failure.
//
// It exercises the branch where NO refusal was singled out, which is the branch
// this package can construct: promotion needs a resolution failure carrying the
// reference it could not resolve, and only a real closure walk produces one. The
// promoted branch is driven end to end by the command-line and tool-surface
// tests instead.
func TestAnnotateSkipContext_LeadingPlusNoteIsTheWholeText(t *testing.T) {
	refused := catalog.SkippedEntity{
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
	failure := fmt.Errorf("resolve: resolve entity %q: %w", "server-recipe:1.0.0", catalog.ErrUnscopedRecipeReference())

	sce, ok := AnnotateSkipContext(failure, []catalog.SkippedEntity{refused}).(*SkipContextError)
	if !ok {
		t.Fatal("AnnotateSkipContext must return the decoration when the record carries a refusal")
	}
	if got, want := sce.Leading().Error()+sce.Note(), sce.Error(); got != want {
		t.Errorf("Leading + Note must reproduce Error\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if !strings.Contains(sce.Note(), `Recipe "server-recipe" (/cat/recipes/helm/server.yaml)`) {
		t.Errorf("the note must name the refused file's declared identity; note:\n%s", sce.Note())
	}
}
