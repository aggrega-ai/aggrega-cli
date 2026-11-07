// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// TestTheLockfileSchemaEnumeratesExactlyTheDeclaredEntityKinds holds the kind a
// lockfile row may record, as the schema the lockfile is validated against
// enumerates it, to the set the compiler declares (closure.DeclaredEntityKinds). The two are
// written in two repositories, so nothing else notices one gaining a word the other
// lacks: a kind only the compiler knows writes rows every load then refuses, and a
// kind only the schema admits is a row no resolution can produce. Each side's
// missing words are named.
func TestTheLockfileSchemaEnumeratesExactlyTheDeclaredEntityKinds(t *testing.T) {
	t.Parallel()
	var doc struct {
		Properties struct {
			Entities struct {
				Items struct {
					Properties struct {
						Kind struct {
							Enum []string `json:"enum"`
						} `json:"kind"`
					} `json:"properties"`
					Required []string `json:"required"`
				} `json:"items"`
			} `json:"entities"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(schemas.LockfileV1alpha1), &doc); err != nil {
		t.Fatalf("parse the lockfile schema: %v", err)
	}
	items := doc.Properties.Entities.Items
	schemaKinds := items.Properties.Kind.Enum
	if len(schemaKinds) == 0 {
		t.Fatal("the lockfile schema enumerates no entity kind, so the comparison below would compare nothing")
	}
	if !slices.Contains(items.Required, "kind") {
		t.Errorf("the lockfile schema does not require an entity row's kind; required = %v", items.Required)
	}
	declared := closure.DeclaredEntityKinds()
	for _, k := range declared {
		if !slices.Contains(schemaKinds, k) {
			t.Errorf("the compiler declares the entity kind %q and the lockfile schema does not admit it", k)
		}
	}
	for _, k := range schemaKinds {
		if !slices.Contains(declared, k) {
			t.Errorf("the lockfile schema admits the entity kind %q and the compiler declares no such kind", k)
		}
	}
}

// TestCheckEntityKindsRefusesARowWithNoDeclaredKind holds the refusal a resolution
// meets before it writes a row the next load would refuse: a row whose kind is
// empty, or is a word that is not a declared kind, is named with the places that
// reference it, and a lockfile whose every row records a declared kind passes.
func TestCheckEntityKindsRefusesARowWithNoDeclaredKind(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		row   lockfile.Entity
		names []string // empty: the lockfile must pass
	}{
		{"a row with a declared kind passes", lockfile.Entity{Kind: closure.KindManifest, URI: "imported/flow"}, nil},
		{"a row with no kind is refused", lockfile.Entity{URI: "imported/flow", ReferencedFrom: []string{"release.spec.manifest"}},
			[]string{`"imported/flow"`, `kind ""`, "referenced from release.spec.manifest"}},
		{"a row carrying a hint that is no kind is refused", lockfile.Entity{Kind: closure.ReleaseAggregateKindHint, URI: "shop"},
			[]string{`"shop"`, `kind "` + closure.ReleaseAggregateKindHint + `"`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lf := &lockfile.Lockfile{Entities: []lockfile.Entity{
				{Kind: closure.KindComponent, URI: "core/api"},
				tc.row,
			}}
			err := lockfile.CheckEntityKinds(lf)
			if len(tc.names) == 0 {
				if err != nil {
					t.Fatalf("a lockfile whose every row records a declared kind was refused: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("a lockfile with the row %+v was accepted", tc.row)
			}
			for _, want := range tc.names {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q:\n%v", want, err)
				}
			}
			if strings.Contains(err.Error(), `"core/api"`) {
				t.Errorf("the refusal names the row that records a declared kind:\n%v", err)
			}
		})
	}
}
