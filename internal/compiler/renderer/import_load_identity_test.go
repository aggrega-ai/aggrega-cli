// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestImportLoadNamesTheDocumentLoadCondition pins the identity each of this
// package's three `$import:` resolvers reports when the file the directive names
// cannot be acquired.
//
// The three surfaces are asserted in one table because the claim is that they
// report ONE condition: a row per surface with a shared expectation is what makes
// a later divergence fail here rather than being discovered by a reader meeting
// two different codes for one mistake.
//
// The match is through the condition's exported accessor rather than against the
// registered code string. The string comparison type-checks and reads like a
// match, and it keeps compiling after the condition is re-registered under a
// different code — at which point it silently stops matching with nothing failing
// and nothing logged.
func TestImportLoadNamesTheDocumentLoadCondition(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-there.tmpl")

	cases := []struct {
		name    string
		resolve func() (string, error)
		clause  string
	}{
		{
			name: "valuesMapping file import",
			resolve: func() (string, error) {
				return resolveValuesMappingText(
					types.ValuesMapping{Import: missing}, "some-recipe", "Recipe", nil)
			},
			clause: `Recipe "some-recipe" valuesMapping file import`,
		},
		{
			name: "workload file import",
			resolve: func() (string, error) {
				return resolveWorkloadText(
					types.WorkloadTemplate{Import: missing}, "some-recipe", nil)
			},
			clause: `Recipe "some-recipe" workload file import`,
		},
		{
			name: "artifacts entry file import",
			resolve: func() (string, error) {
				return resolveArtifactEntryText(
					types.ArtifactEntry{Import: missing}, "some-recipe", "overlay.yaml")
			},
			clause: `Recipe "some-recipe" artifact "overlay.yaml" file import`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, err := tc.resolve()
			if err == nil {
				t.Fatalf("resolving an import that names no file returned %q and no error", text)
			}
			if !errors.Is(err, resolver.ErrYAMLLoad()) {
				t.Errorf("error does not report the document-load condition: %v", err)
			}
			code, ok := cerrors.ShortCodeOf(err)
			if !ok || code == "" {
				t.Errorf("chain names no registered condition, so the diagnostic head carries no short code: %v", err)
			}
			// The diagnostic's own message leads with the registered sentence,
			// because the boundary that renders a halt prints the outermost frame
			// whose text the output does not already carry — a message that did
			// not lead with it would print the sentence again as a redundant
			// continuation line beneath the diagnostic. The assertion is over the
			// envelope's message rather than over Error(), which additionally
			// carries the category and the source the head renders separately.
			var ce *cerrors.CompilerError
			if !errors.As(err, &ce) {
				t.Fatalf("error is not the canonical envelope, so the boundary renders no head for it: %v", err)
			}
			sentence, _ := cerrors.MessageOf(resolver.ErrYAMLLoad())
			if !strings.HasPrefix(ce.Message(), sentence) {
				t.Errorf("message does not open with the registered sentence:\n got: %s\nwant prefix: %s", ce.Message(), sentence)
			}
			if n := strings.Count(ce.Message(), sentence); n != 1 {
				t.Errorf("registered sentence appears %d times in the message; want exactly 1: %s", n, ce.Message())
			}
			if !strings.Contains(ce.Message(), tc.clause) {
				t.Errorf("message does not name the surface that declared the import:\n got: %s\nwant to contain: %s", ce.Message(), tc.clause)
			}
			if !strings.Contains(ce.Message(), missing) {
				t.Errorf("message does not name the path that could not be read:\n got: %s", ce.Message())
			}
		})
	}
}

// TestImportLoadIsNotReportedForAnInlineOrLibraryEntry keeps the condition bound
// to the failure it names. An inline entry reads no file, and a library import is
// resolved from the loaded closure rather than from disk, so neither can report a
// file that could not be read — and a row that reported it anyway would widen the
// condition past the subject its sentence states.
func TestImportLoadIsNotReportedForAnInlineOrLibraryEntry(t *testing.T) {
	text, err := resolveValuesMappingText(
		types.ValuesMapping{Inline: "a: b"}, "some-recipe", "Recipe", nil)
	if err != nil {
		t.Fatalf("inline valuesMapping: %v", err)
	}
	if text != "a: b" {
		t.Errorf("inline valuesMapping text = %q; want %q", text, "a: b")
	}

	_, err = resolveWorkloadText(
		types.WorkloadTemplate{Import: "absent-library#/workloads/entry"}, "some-recipe", nil)
	if err == nil {
		t.Fatalf("a library import naming no loaded library returned no error")
	}
	if errors.Is(err, resolver.ErrYAMLLoad()) {
		t.Errorf("a library import that resolved to nothing reports a file-load failure: %v", err)
	}
}
