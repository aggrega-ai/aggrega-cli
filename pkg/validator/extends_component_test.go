// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

// mustParseSchema parses a YAML string into a yaml.Node suitable for use
// as a ComponentSpec.Schema / FeatureSpec.Schema field. Test helper.
func mustParseSchema(t *testing.T, src string) yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	// yaml.Unmarshal returns a DocumentNode at top-level; the actual content
	// is the first child. Use the child so the Schema field carries a
	// mapping node directly.
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		return *doc.Content[0]
	}
	return doc
}

// schemaContains marshals a Schema node and substring-checks the result.
// Tolerates YAML formatting drift (whitespace, key order, scalar style).
func schemaContains(t *testing.T, n yaml.Node, want string) bool {
	t.Helper()
	out, err := yaml.Marshal(&n)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	return strings.Contains(string(out), want)
}

func TestResolveComponentExtends_NoExtends(t *testing.T) {
	t.Parallel()
	schema := mustParseSchema(t, `values:
  port: { type: integer }
`)
	comp := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/standalone", Version: "1.0.0"},
		Spec:     types.ComponentSpec{Schema: schema},
	}
	got, errs := ResolveComponentExtends(comp, func(string) (types.Component, bool) { return types.Component{}, false })
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if !schemaContains(t, got.Spec.Schema, "port") {
		t.Errorf("no-extends should return unchanged comp")
	}
}

func TestResolveComponentExtends_MergesLabelsAndListFields(t *testing.T) {
	t.Parallel()
	labels := types.OrderedMap[string]{}
	labels.Set(types.ComponentTypeLabel, "server")
	labels.Set("tier", "base")
	usesSchema := types.OrderedMap[types.UsesSchemaEntry]{}
	usesSchema.Set("databases", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=stateful-server"})
	parent := types.Component{
		Metadata: types.ComponentMetadata{
			Name:    "core/compute/server",
			Version: "1.0.0",
			Labels:  labels,
		},
		Spec: types.ComponentSpec{
			Inject:     []string{"env"},
			UsesSchema: usesSchema,
			Schema: mustParseSchema(t, `values:
  port: { type: integer, minimum: 1024 }
`),
		},
	}
	labels2 := types.OrderedMap[string]{}
	labels2.Set("tier", "prod")
	usesSchema2 := types.OrderedMap[types.UsesSchemaEntry]{}
	usesSchema2.Set("caches", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=resource"})
	child := types.Component{
		Metadata: types.ComponentMetadata{
			Name:    "core/compute/api",
			Version: "1.0.0",
			Labels:  labels2, // overrides parent tier=base
		},
		Spec: types.ComponentSpec{
			Extends:    "core/compute/server",
			Inject:     []string{"env", "file"},
			UsesSchema: usesSchema2,
			Schema: mustParseSchema(t, `values:
  name: { type: string }
`),
		},
	}

	lookup := func(uri string) (types.Component, bool) {
		if uri == "core/compute/server" {
			return parent, true
		}
		return types.Component{}, false
	}

	got, errs := ResolveComponentExtends(child, lookup)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	// Merged labels: parent keys present, child overrides shared keys
	typeLbl, _ := got.Metadata.Labels.Get("component.aggrega.ai/type")
	if typeLbl != "server" {
		t.Errorf("expected inherited type label, got %v", got.Metadata.Labels.Keys())
	}
	tierLbl, _ := got.Metadata.Labels.Get("tier")
	if tierLbl != "prod" {
		t.Errorf("child should win on tier, got %q", tierLbl)
	}

	// Inject dedupes "env"
	if len(got.Spec.Inject) != 2 {
		t.Errorf("expected 2 inject mechanisms, got %v", got.Spec.Inject)
	}

	// Merged usesSchema: both sockets present
	if _, ok := got.Spec.UsesSchema.Get("databases"); !ok {
		t.Error("expected inherited 'databases' socket")
	}
	if _, ok := got.Spec.UsesSchema.Get("caches"); !ok {
		t.Error("expected child 'caches' socket")
	}

	// Schema carries the child's as-authored content. JSON Schema
	// composition across the extends chain runs through the merger
	// package against the structured Schema payloads (Spec.JSONSchema);
	// the mergeComponent step preserves the child's Spec.Schema as-is.
	if !schemaContains(t, got.Spec.Schema, "name") {
		t.Errorf("expected child's authored Schema preserved")
	}

	// Identity stays child's
	if got.Metadata.Name != "core/compute/api" {
		t.Errorf("identity should be child's name, got %q", got.Metadata.Name)
	}
}

func TestResolveComponentExtends_MergesFeaturesSchema(t *testing.T) {
	t.Parallel()
	featuresSchema := types.OrderedMap[types.AttachmentSlot]{}
	featuresSchema.Set("root_password", types.AttachmentSlot{Required: true, Inject: []string{"env"}})
	featuresSchema.Set("tls_ca", types.AttachmentSlot{Required: false})
	parent := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/db/base", Version: "1.0.0"},
		Spec: types.ComponentSpec{
			FeaturesSchema: featuresSchema,
		},
	}
	featuresSchema2 := types.OrderedMap[types.AttachmentSlot]{}
	featuresSchema2.Set("root_password", types.AttachmentSlot{Required: true, Inject: []string{"env"}, Description: "strict"})
	featuresSchema2.Set("api_token", types.AttachmentSlot{Required: true})
	child := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/db/strict", Version: "1.0.0"},
		Spec: types.ComponentSpec{
			Extends:        "core/db/base",
			FeaturesSchema: featuresSchema2,
		},
	}
	lookup := func(uri string) (types.Component, bool) {
		if uri == "core/db/base" {
			return parent, true
		}
		return types.Component{}, false
	}
	got, errs := ResolveComponentExtends(child, lookup)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	rootSlot, _ := got.Spec.FeaturesSchema.Get("root_password")
	if rootSlot.Description != "strict" {
		t.Errorf("child slot should override parent's, got %v", rootSlot)
	}
	if _, ok := got.Spec.FeaturesSchema.Get("tls_ca"); !ok {
		t.Error("parent-only slot lost during merge")
	}
	if _, ok := got.Spec.FeaturesSchema.Get("api_token"); !ok {
		t.Error("child-only slot missing")
	}
}

func TestResolveComponentExtends_ChildOverridesUsesSchema(t *testing.T) {
	t.Parallel()
	usesSchema := types.OrderedMap[types.UsesSchemaEntry]{}
	usesSchema.Set("databases", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=generic"})
	parent := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/base", Version: "1.0.0"},
		Spec: types.ComponentSpec{
			UsesSchema: usesSchema,
		},
	}
	usesSchema2 := types.OrderedMap[types.UsesSchemaEntry]{}
	usesSchema2.Set("databases", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=stateful-server"})
	child := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/strict", Version: "1.0.0"},
		Spec: types.ComponentSpec{
			Extends:    "core/base",
			UsesSchema: usesSchema2,
		},
	}
	lookup := func(uri string) (types.Component, bool) {
		if uri == "core/base" {
			return parent, true
		}
		return types.Component{}, false
	}
	got, errs := ResolveComponentExtends(child, lookup)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	dbEntry, _ := got.Spec.UsesSchema.Get("databases")
	if dbEntry.Selector != "component.aggrega.ai/type=stateful-server" {
		t.Errorf("child usesSchema entry should override parent, got %q", dbEntry.Selector)
	}
}

func TestResolveComponentExtends_CyclicExtendsEmitsSentinel(t *testing.T) {
	t.Parallel()
	a := types.Component{
		Metadata: types.ComponentMetadata{Name: "a", Version: "1.0.0"},
		Spec:     types.ComponentSpec{Extends: "b"},
	}
	b := types.Component{
		Metadata: types.ComponentMetadata{Name: "b", Version: "1.0.0"},
		Spec:     types.ComponentSpec{Extends: "a"},
	}
	lookup := func(uri string) (types.Component, bool) {
		switch uri {
		case "a":
			return a, true
		case "b":
			return b, true
		}
		return types.Component{}, false
	}
	_, errs := ResolveComponentExtends(a, lookup)
	if len(errs) == 0 {
		t.Fatal("expected cycle error")
	}
	if !errors.Is(errs[0], ErrCyclicExtension) {
		t.Errorf("expected ErrCyclicExtension, got %v", errs[0].Err)
	}
}

func TestResolveComponentExtends_MissingParent(t *testing.T) {
	t.Parallel()
	child := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/child", Version: "1.0.0"},
		Spec:     types.ComponentSpec{Extends: "core/vanished"},
	}
	_, errs := ResolveComponentExtends(child, func(string) (types.Component, bool) {
		return types.Component{}, false
	})
	if len(errs) == 0 {
		t.Fatal("expected missing-parent error")
	}
	if !errors.Is(errs[0], ErrInheritanceMerge) {
		t.Errorf("expected ErrInheritanceMerge, got %v", errs[0].Err)
	}
}

func TestResolveFeatureExtends_Basic(t *testing.T) {
	t.Parallel()
	parent := types.Feature{
		Metadata: types.FeatureMetadata{Name: "core/monitoring/base", Version: "1.0.0"},
		Spec: types.FeatureSpec{
			Schema: mustParseSchema(t, `values:
  scrape_port: { type: integer }
`),
		},
	}
	child := types.Feature{
		Metadata: types.FeatureMetadata{Name: "core/monitoring/strict", Version: "1.0.0"},
		Spec: types.FeatureSpec{
			Extends: "core/monitoring/base",
			Schema: mustParseSchema(t, `values:
  scrape_port: { type: integer, minimum: 9000 }
`),
		},
	}
	lookup := func(uri string) (types.Feature, bool) {
		if uri == "core/monitoring/base" {
			return parent, true
		}
		return types.Feature{}, false
	}
	got, errs := ResolveFeatureExtends(child, lookup)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if !schemaContains(t, got.Spec.Schema, "9000") {
		t.Errorf("child constraint missing in resolved schema")
	}
}
