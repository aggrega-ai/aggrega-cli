// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"errors"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// TestModuleComponentReleaseFindingsResolveToTheAuthoredKey proves a finding
// about what a Release wrote against a component a sub-module contributed is
// positioned at a key the Release document holds: its locator, walked through the
// editor's position recovery over the document as written, reaches the key it
// names, and the finding names the Release's file.
//
// The two rows are the two ways a Release reaches such a component. Through a
// routing entry the component is keyed by its local name, so the locator spells
// the whole path to the socket. Under the identifier the composition gave it the
// key holds the locator dialect's separator, so the locator names the components
// block holding that key and stops, and the finding names the component and the
// attachment in its detail instead.
func TestModuleComponentReleaseFindingsResolveToTheAuthoredKey(t *testing.T) {
	t.Parallel()
	const moduleSpec = `  components:
    api:
      type: app/env-only:1.0.0
`
	for _, tc := range []struct {
		name        string
		boundary    string
		wantLocator string
		wantKey     string
	}{
		{
			name: "a routing entry keys the component by its local name",
			boundary: `      aggregates:
        mod:
          components:
            api:
              features:
                scrape:
                  type: app/scrape:1.0.0
                  uses:
                    invented-socket:
                      link:
                        target: mod.api
`,
			wantLocator: ".spec.boundaries.edge.aggregates.mod.components.api.features.scrape.uses.invented-socket",
			wantKey:     "invented-socket",
		},
		{
			name: "the composed identifier as the key stops at the components block",
			boundary: `      components:
        mod.api:
          features:
            scrape:
              type: app/scrape:1.0.0
              uses:
                invented-socket:
                  link:
                    target: mod.api
`,
			wantLocator: ".spec.boundaries.edge.components",
			wantKey:     "components",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src := "apiVersion: aggrega.ai/v1beta1\nkind: Release\nmetadata:\n  name: rel\n" +
				"spec:\n  aggregate: root\n  boundaries:\n    edge:\n" + tc.boundary
			var release types.Release
			if err := yaml.Unmarshal([]byte(src), &release); err != nil {
				t.Fatalf("release fixture parse: %v\n%s", err, src)
			}
			var doc yaml.Node
			if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
				t.Fatalf("release fixture node parse: %v", err)
			}

			err := buildModuleCompositionAt(t, []string{"mod"},
				map[string]string{"mods/data": moduleSpec}, &release, "release.yaml", "")
			if !errors.Is(err, resolver.ErrUnknownUsesSocket()) {
				t.Fatalf("want the socket rule to halt; got %v", err)
			}
			var multi *resolver.MultiResolverError
			if !errors.As(err, &multi) {
				t.Fatalf("want a grouped refusal; got %v", err)
			}
			var found *resolver.ResolverError
			for _, v := range multi.Violations {
				if errors.Is(v, resolver.ErrUnknownUsesSocket()) && v.Entity == "Release rel" {
					found = v
					break
				}
			}
			if found == nil {
				t.Fatalf("no finding under the socket rule names the Release; got %v", err)
			}
			if found.File != "release.yaml" {
				t.Errorf("the finding names file %q; want the Release's own, release.yaml", found.File)
			}
			if found.Field != tc.wantLocator {
				t.Errorf("locator = %q; want %q", found.Field, tc.wantLocator)
			}
			node := yamlpos.FieldNode(&doc, found.Field)
			if node == nil || node.Value != tc.wantKey {
				got := "<nothing>"
				if node != nil {
					got = node.Value
				}
				t.Errorf("the locator %q resolves to %s in the Release as written; want the key %q",
					found.Field, got, tc.wantKey)
			}
			for _, want := range []string{`"mod.api"`, `"scrape"`, `"invented-socket"`} {
				if !strings.Contains(found.Message, want) {
					t.Errorf("the finding's detail does not name %s; got %q", want, found.Message)
				}
			}
		})
	}
}
