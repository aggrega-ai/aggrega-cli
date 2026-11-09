// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"errors"
	"fmt"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// moduleRulesCatalog holds the types the module fixtures below author against:
// a component type permitting delivery through env alone, and a Feature
// declaring no socket.
func moduleRulesCatalog(t *testing.T) resolver.CatalogLookup {
	t.Helper()
	var envOnly types.Component
	if err := yaml.Unmarshal([]byte(`apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: app/env-only
  version: "1.0.0"
spec:
  inject: [env]
`), &envOnly); err != nil {
		t.Fatalf("component fixture parse: %v", err)
	}
	var scrape types.Feature
	if err := yaml.Unmarshal([]byte(`apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: app/scrape
  version: "1.0.0"
spec: {}
`), &scrape); err != nil {
		t.Fatalf("feature fixture parse: %v", err)
	}
	return resolver.CatalogLookup{
		Component: func(u string) (types.Component, bool) {
			if u == "app/env-only:1.0.0" {
				return envOnly, true
			}
			return types.Component{}, false
		},
		Feature: func(u string) (types.Feature, bool) {
			if u == "app/scrape:1.0.0" {
				return scrape, true
			}
			return types.Feature{}, false
		},
	}
}

// buildWithModule builds a root Aggregate importing one module, the document
// moduleSpec spells, under the name `mod`, with the Release given (nil for
// none). The module's one component `api` is placed in boundary `edge`.
func buildWithModule(t *testing.T, moduleSpec string, release *types.Release) error {
	t.Helper()
	return buildModuleComposition(t, []string{"mod"}, map[string]string{"mods/data": moduleSpec}, release)
}

// buildModuleComposition builds a root Aggregate importing the module type mods/data
// once under each name in imports, against the module documents given by type
// (the spec block of each, which may itself import another of them), with the
// Release given (nil for none). The component `api` of each import is placed in
// boundary `edge`.
func buildModuleComposition(t *testing.T, imports []string, modules map[string]string, release *types.Release) error {
	t.Helper()
	return buildModuleCompositionAt(t, imports, modules, release, "", "")
}

// buildModuleCompositionAt is buildModuleComposition with the Release named by
// the file path given, as a workspace build names it, and every module document
// read from moduleFile, as a catalog-backed loader names the file it read.
func buildModuleCompositionAt(
	t *testing.T, imports []string, modules map[string]string, release *types.Release, releasePath, moduleFile string,
) error {
	t.Helper()
	documents := map[string]*types.Aggregate{}
	for typeName, spec := range modules {
		var module types.Aggregate
		doc := "apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\nmetadata:\n  name: " + typeName + "\nspec:\n" + spec
		if err := yaml.Unmarshal([]byte(doc), &module); err != nil {
			t.Fatalf("module fixture parse: %v\n%s", err, doc)
		}
		documents[typeName] = &module
	}
	aggregates := types.OrderedMap[types.SubAggregate]{}
	placed := map[string]*resolver.BoundaryInfra{}
	for _, name := range imports {
		aggregates.Set(name, types.SubAggregate{Type: "mods/data"})
		placed[name+".api"] = &resolver.BoundaryInfra{BoundaryPath: "edge"}
	}
	root := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "root"},
		Spec:     types.AggregateSpec{Aggregates: aggregates},
	}
	loader := func(typeName string, _ map[string]any) (*types.Aggregate, string, map[string]any, error) {
		if module, ok := documents[typeName]; ok {
			return module, moduleFile, nil, nil
		}
		return nil, "", nil, fmt.Errorf("unknown: %s", typeName)
	}
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("the build panicked: %v", r)
			}
		}()
		_, err = Build(BuildConfig{
			ResolveResult: &resolver.ResolveResult{
				Aggregate:     root,
				Release:       release,
				ReleasePath:   releasePath,
				BoundaryInfra: placed,
			},
			LoadSubAggregate: loader,
			CatalogLookup:    moduleRulesCatalog(t),
		})
	}()
	return err
}

// TestBuildAsksTheModuleRulesOfEachModuleDocument proves the composition asks
// the rules over an authoring document's own declarations of a sub-module's
// document: a module component delivering through a mechanism its type does not
// permit, and a module subsystem attachment wiring a socket its Feature does not
// declare, each halt the build under the condition the same declaration halts
// under in the Aggregate, naming the module's document and the key inside it.
// Removing the module pass from the composition fails both rows.
//
// The control row is the same module with no fault, which must build.
func TestBuildAsksTheModuleRulesOfEachModuleDocument(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		spec    string
		want    error
		locator string
	}{
		{
			name: "a well-formed module builds",
			spec: `  components:
    api:
      type: app/env-only:1.0.0
      inject:
        env:
          - { in: API_MODE, value: "x" }
`,
		},
		{
			name: "a module component delivering through a mechanism its type does not permit",
			spec: `  components:
    api:
      type: app/env-only:1.0.0
      inject:
        file:
          - { in: /etc/probe, value: "x" }
`,
			want:    resolver.ErrSelfInjectForbidden(),
			locator: ".spec.components.api.inject.file",
		},
		{
			name: "a module subsystem attachment wiring a socket its Feature does not declare",
			spec: `  subsystems:
    store:
      features:
        scrape:
          type: app/scrape:1.0.0
          uses:
            invented-socket:
              link:
                target: api
      components:
        api:
          type: app/env-only:1.0.0
`,
			want:    resolver.ErrUnknownUsesSocket(),
			locator: ".spec.subsystems.store.features.scrape",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := buildWithModule(t, tc.spec, nil)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("the well-formed module must build; got %v", err)
				}
				return
			}
			assertFinding(t, err, tc.want, "Aggregate mods/data", tc.locator)
		})
	}
}

// assertFinding asserts the build halted with a grouped refusal holding a finding
// under the given condition that names the given document and sits at the given
// key. The group's own rendering carries only a count of its findings, so each
// finding is read from the group rather than from that rendering.
func assertFinding(t *testing.T, err error, want error, document, locator string) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("want %v; got %v", want, err)
	}
	var multi *resolver.MultiResolverError
	if !errors.As(err, &multi) {
		t.Fatalf("want a grouped refusal; got %v", err)
	}
	var seen []string
	for _, v := range multi.Violations {
		if !errors.Is(v, want) {
			continue
		}
		if v.Entity == document && v.Field == locator {
			return
		}
		seen = append(seen, v.Entity+" at "+v.Field)
	}
	t.Errorf("no finding under %v names %q at %q; the findings under it name %v", want, document, locator, seen)
}

// TestBuildAsksTheReleaseAttachmentSocketRuleOfAModuleComponent proves an
// attachment the Release writes against a component a sub-module contributed is
// held to the socket rule: wiring a socket the Feature does not declare halts,
// naming the Release and the attachment. Removing the arm fails the first row.
//
// The control row writes the same attachment with no `uses`, and must not halt
// under the socket rule.
func TestBuildAsksTheReleaseAttachmentSocketRuleOfAModuleComponent(t *testing.T) {
	t.Parallel()
	const moduleSpec = `  components:
    api:
      type: app/env-only:1.0.0
`
	release := func(t *testing.T, attachment string) *types.Release {
		t.Helper()
		var r types.Release
		doc := `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: rel
spec:
  aggregate: root
  boundaries:
    edge:
      components:
        mod.api:
          features:
            scrape:
              type: app/scrape:1.0.0
` + attachment
		if err := yaml.Unmarshal([]byte(doc), &r); err != nil {
			t.Fatalf("release fixture parse: %v\n%s", err, doc)
		}
		return &r
	}

	err := buildWithModule(t, moduleSpec, release(t, `              uses:
                invented-socket:
                  link:
                    target: mod.api
`))
	// The Release keys the component by the identifier the composition gave it,
	// which holds the locator dialect's separator, so the finding is positioned at
	// the components block holding that key.
	assertFinding(t, err, resolver.ErrUnknownUsesSocket(),
		"Release rel", `.spec.boundaries.edge.components`)

	if err := buildWithModule(t, moduleSpec, release(t, "")); errors.Is(err, resolver.ErrUnknownUsesSocket()) {
		t.Fatalf("an attachment wiring no socket must not halt under the socket rule; got %v", err)
	}
}

// forbiddenFileDelivery is a module component delivering through a mechanism its
// type does not permit, the fault the two tests below plant.
const forbiddenFileDelivery = `  components:
    api:
      type: app/env-only:1.0.0
      inject:
        file:
          - { in: /etc/probe, value: "x" }
`

// TestBuildAsksTheModuleRulesOfANestedModule proves a module imported by a module,
// rather than by the Aggregate, has its own document asked too: the fault sits
// in the inner module, and the halt names the inner module's document. Dropping
// the nested modules from the composition's record fails it.
func TestBuildAsksTheModuleRulesOfANestedModule(t *testing.T) {
	t.Parallel()
	err := buildModuleComposition(t, []string{"mod"}, map[string]string{
		"mods/data": `  components:
    api:
      type: app/env-only:1.0.0
  aggregates:
    inner:
      type: mods/inner
`,
		"mods/inner": forbiddenFileDelivery,
	}, nil)
	assertFinding(t, err, resolver.ErrSelfInjectForbidden(), "Aggregate mods/inner", ".spec.components.api.inject.file")
}

// TestBuildReportsAModuleImportedTwiceOnce proves one fault in a module the
// composition imports twice is reported once: both imports read the same
// document, so the finding names the same document and key, and the second copy
// would ask the author to make one edit twice.
func TestBuildReportsAModuleImportedTwiceOnce(t *testing.T) {
	t.Parallel()
	err := buildModuleComposition(t, []string{"first", "second"},
		map[string]string{"mods/data": forbiddenFileDelivery}, nil)
	var multi *resolver.MultiResolverError
	if !errors.As(err, &multi) {
		t.Fatalf("want a grouped refusal; got %v", err)
	}
	n := 0
	for _, v := range multi.Violations {
		if errors.Is(v, resolver.ErrSelfInjectForbidden()) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the module's one fault was reported %d times, want once: %v", n, err)
	}
}
