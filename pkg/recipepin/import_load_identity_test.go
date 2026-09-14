// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package recipepin_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestArtifactPinNamesTheDocumentLoadCondition pins what the pin freeze reports
// when an artifacts entry's `$import:` names a file it cannot read.
//
// The pin freeze and the render read the same file through the same reader, so
// the two report one condition — and this asserts it through the condition's
// exported accessor rather than against the registered code string, which
// type-checks and reads like a match while silently ceasing to match if the
// condition is ever re-registered under a different code.
//
// It also asserts the diagnostic IS the canonical envelope. That is the property
// that gives this boundary a bracketed head at all: the boundary rendering a halt
// prints a head for an error that is or wraps the envelope, and falls through to
// plain single-line text for one that is not — so a plain wrapped error here
// would carry the identity in the chain and still print nothing a reader can
// quote.
func TestArtifactPinNamesTheDocumentLoadCondition(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-there.tmpl")

	var arts types.OrderedMap[types.ArtifactEntry]
	arts.Set("overlay.yaml", types.ArtifactEntry{Import: missing})

	pins, err := recipepin.ArtifactPinsForRecipe(arts, "Recipe", "pinned-recipe")
	if err == nil {
		t.Fatalf("pinning an import that names no file returned %v and no error", pins)
	}
	if !errors.Is(err, resolver.ErrYAMLLoad()) {
		t.Errorf("error does not report the document-load condition: %v", err)
	}
	code, ok := cerrors.ShortCodeOf(err)
	if !ok || code == "" {
		t.Errorf("chain names no registered condition, so the diagnostic head carries no short code: %v", err)
	}

	var ce *cerrors.CompilerError
	if !errors.As(err, &ce) {
		t.Fatalf("error is not the canonical envelope, so the boundary renders no head for it: %v", err)
	}
	sentence, _ := cerrors.MessageOf(resolver.ErrYAMLLoad())
	if !strings.HasPrefix(ce.Message(), sentence) {
		t.Errorf("message does not open with the registered sentence:\n got: %s\nwant prefix: %s", ce.Message(), sentence)
	}
	if ce.Source().File != missing {
		t.Errorf("source pointer = %q; want the path that could not be read, %q", ce.Source().File, missing)
	}
	for _, want := range []string{`Recipe "pinned-recipe"`, `artifact "overlay.yaml" file import`, missing} {
		if !strings.Contains(ce.Message(), want) {
			t.Errorf("message does not name %q:\n got: %s", want, ce.Message())
		}
	}
	// The message is written in what the author typed, so no package name of the
	// compiler's own reaches a surface a reader meets.
	if strings.Contains(ce.Message(), "recipepin") {
		t.Errorf("message names the Go package that raised it:\n got: %s", ce.Message())
	}
}

// TestArtifactPinReportsNothingForInlineOrLibraryEntries keeps the condition
// bound to the failure it names: neither an inline entry nor a library import
// reads a file from disk here, so neither can report one that could not be read.
func TestArtifactPinReportsNothingForInlineOrLibraryEntries(t *testing.T) {
	var arts types.OrderedMap[types.ArtifactEntry]
	arts.Set("inline.yaml", types.ArtifactEntry{Inline: "a: b"})
	arts.Set("bundled.yaml", types.ArtifactEntry{Import: "shared-bundle#/artifacts/overlay"})

	pins, err := recipepin.ArtifactPinsForRecipe(arts, "FeatureRecipe", "pinned-feature-recipe")
	if err != nil {
		t.Fatalf("ArtifactPinsForRecipe: %v", err)
	}
	if len(pins) != 0 {
		t.Errorf("want no pins (inline and library entries carry none), got %d: %v", len(pins), pins)
	}
}
