// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// Where the editor draws the finding a check of one catalog entity reports: on
// the key the finding's own chain names in the entity, and on the entity's kind:
// key where it names none — never on the licence comment a catalog file opens
// with.
//
// Every expected position is read off the fixture's text — the line each key is
// written on — and never off the code under test.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestTheEntityCheckDrawsItsFindingWhereItsChainPoints drives aggrega/validate
// over the real framing for a Recipe opening with a licence comment, checked on
// its own in the terms of a Release whose catalog holds the Component it
// implements, one fault per row.
//
// The assertion row is the shape a catalog Recipe declaring a runtime
// prerequisite takes: checked on its own, nothing affirms the prerequisite, so
// the assertion fails, and the entry is drawn on that assertion's own key. The
// other rows are findings whose chain names no key in the Recipe — an implements
// target the catalog does not hold, and a Release that is not there — and they
// are drawn on the Recipe's kind: key.
func TestTheEntityCheckDrawsItsFindingWhereItsChainPoints(t *testing.T) {
	t.Parallel()
	srv, root := entityCheckWorkspace(t)
	fixture := mustRead(t, filepath.Join("testdata", "entity-check-position", "recipe.yaml"))
	if !strings.HasPrefix(fixture, "#") {
		t.Fatal("the fixture no longer opens with a comment, so the head of the file and the comment no longer coincide")
	}
	recipe := filepath.Join(root, "variant-recipe", "recipe", "helm", "rabbitmq-cluster", "1.0.0", "recipe.yaml")
	writeFixtureFile(t, recipe, fixture)
	assertionCode, _ := cerrors.CodeOf(renderer.ErrAssertionFailed())
	if assertionCode == "" {
		t.Fatal("the assertion condition carries no code, so the row asserting it would be vacuous")
	}

	const assertion = "rabbitmq_operator_prerequisite"
	cases := []struct {
		name    string
		release string
		// edit, when set, turns the fixture into the buffer the request carries.
		edit func(string) string
		// carries is a phrase of the entry under test, which is how it is found.
		carries string
		// key is the key the entry must be drawn on, as the fixture spells it at the
		// head of its line.
		key string
		// keyPath is the key path the entry must publish, or "" for none.
		keyPath string
		// code is the condition the entry must name, or "" where the row does not
		// read one.
		code string
	}{
		{
			name:    "a failing Recipe assertion is drawn on its own key",
			release: "release-recipe.yaml",
			carries: "the RabbitMQ Cluster Operator must be installed",
			key:     assertion + ":",
			keyPath: ".spec.assertions." + assertion,
			code:    assertionCode,
		},
		{
			name:    "a finding naming no key in the Recipe is drawn on its kind: key",
			release: "release-recipe.yaml",
			edit: func(s string) string {
				return strings.Replace(s, "implements: example/compute/service", "implements: example/compute/absent", 1)
			},
			carries: `implements target "example/compute/absent" not found in catalog`,
			key:     "kind:",
		},
		{
			name:    "a Release that is not there is drawn on the Recipe's kind: key",
			release: "release-gone.yaml",
			carries: "the Release release-gone.yaml does not exist",
			key:     "kind:",
		},
	}
	// Not parallel: the rows share one daemon and name one file.
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := fixture
			if tc.edit != nil {
				edited := tc.edit(content)
				if edited == content {
					t.Fatal("the edit changed nothing, so the row would not carry its fault")
				}
				content = edited
			}
			res := validateContentOverTheWire(t, srv, recipe, content, tc.release)
			var found []Diagnostic
			for _, d := range res.Diagnostics {
				if strings.Contains(d.Message, tc.carries) {
					found = append(found, d)
				}
			}
			if len(found) != 1 {
				t.Fatalf("want one entry carrying %q; the answer carries %q", tc.carries, diagMessages(res.Diagnostics))
			}
			got := found[0]
			line, column := keyPosition(t, content, tc.key)
			if line == 1 {
				t.Fatalf("the fixture writes %s on its first line, so this row cannot tell it from the head of the file", tc.key)
			}
			endColumn := column + len(strings.TrimSuffix(tc.key, ":"))
			if got.Line != line || got.Column != column || got.EndLine != line || got.EndColumn != endColumn {
				t.Errorf("the entry is drawn at %d:%d-%d:%d; want %s at %d:%d-%d:%d",
					got.Line, got.Column, got.EndLine, got.EndColumn, tc.key, line, column, line, endColumn)
			}
			if got.KeyPath != tc.keyPath {
				t.Errorf("the entry names key path %q; want %q", got.KeyPath, tc.keyPath)
			}
			if tc.code != "" && got.Code != tc.code {
				t.Errorf("the entry names condition %q; want %q", got.Code, tc.code)
			}
		})
	}
}
