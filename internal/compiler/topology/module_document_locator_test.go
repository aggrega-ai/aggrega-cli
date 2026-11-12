// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package topology

import (
	"errors"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// TestModuleComponentFindingsNameTheModuleDocument proves a finding about what a
// module's component declares names the module's own document and is positioned
// at the key that document holds: the finding names the module, carries the file
// the module was read from, and its locator, walked through the editor's position
// recovery over the module document as written, reaches the key it names — under
// the component's local name and the module's own subsystem path, never the
// identifier the composition gave the component.
//
// The two rows are the two places a module writes a component: at its top level,
// which normalization folds into a scope it synthesized and the locator does not
// name, and inside a subsystem the module declares, which the locator names level
// by level.
func TestModuleComponentFindingsNameTheModuleDocument(t *testing.T) {
	t.Parallel()
	const attachment = `type: app/env-only:1.0.0
features:
  scrape:
    type: app/scrape:1.0.0
    uses:
      invented-socket:
        link:
          target: api
`
	for _, tc := range []struct {
		name        string
		spec        string
		wantLocator string
	}{
		{
			name:        "a component at the module's top level",
			spec:        "  components:\n    api:\n" + indent(attachment, "      "),
			wantLocator: ".spec.components.api.features.scrape.uses.invented-socket",
		},
		{
			name:        "a component inside a subsystem the module declares",
			spec:        "  subsystems:\n    store:\n      components:\n        api:\n" + indent(attachment, "          "),
			wantLocator: ".spec.subsystems.store.components.api.features.scrape.uses.invented-socket",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			const moduleFile = "catalog/aggregate/mods/data/1.0.0/aggregate.yaml"
			err := buildModuleCompositionAt(t, []string{"mod"},
				map[string]string{"mods/data": tc.spec}, nil, "", moduleFile)
			if !errors.Is(err, resolver.ErrUnknownUsesSocket()) {
				t.Fatalf("want the socket rule to halt; got %v", err)
			}
			var multi *resolver.MultiResolverError
			if !errors.As(err, &multi) {
				t.Fatalf("want a grouped refusal; got %v", err)
			}
			var found *resolver.ResolverError
			for _, v := range multi.Violations {
				if errors.Is(v, resolver.ErrUnknownUsesSocket()) {
					found = v
					break
				}
			}
			if found == nil {
				t.Fatalf("no finding under the socket rule; got %v", err)
			}
			if found.Entity != "Aggregate mods/data" {
				t.Errorf("the finding names %q; want the module's own document, Aggregate mods/data", found.Entity)
			}
			if found.File != moduleFile {
				t.Errorf("the finding carries file %q; want the module's, %q", found.File, moduleFile)
			}
			if found.Field != tc.wantLocator {
				t.Errorf("locator = %q; want %q", found.Field, tc.wantLocator)
			}
			// The module document exactly as the composition read it.
			src := "apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\nmetadata:\n  name: mods/data\nspec:\n" + tc.spec
			var doc yaml.Node
			if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
				t.Fatalf("module document parse: %v", err)
			}
			node := yamlpos.FieldNode(&doc, found.Field)
			if node == nil || node.Value != "invented-socket" {
				got := "<nothing>"
				if node != nil {
					got = node.Value
				}
				t.Errorf("the locator %q resolves to %s in the module document; want the key invented-socket",
					found.Field, got)
			}
			if strings.Contains(found.Error(), "Aggregate root") {
				t.Errorf("the finding names the importing document: %v", found)
			}
		})
	}
}

// indent prefixes every line of block with pad.
func indent(block, pad string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(block, "\n"), "\n") {
		b.WriteString(pad + line + "\n")
	}
	return b.String()
}
