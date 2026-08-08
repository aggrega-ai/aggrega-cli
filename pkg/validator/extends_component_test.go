// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"errors"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
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
			// An extending Component authors no pillar content; its Spec.Schema
			// carries the inheritance marker on disk.
			Schema: mustParseSchema(t, InheritedSchemaMarker),
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

	// Schema is the parent's node, copied down verbatim: the child's own
	// marker is replaced, so the resolved Component's authored schema IS the
	// schema every enforcement path reads.
	if !schemaContains(t, got.Spec.Schema, "port") {
		t.Errorf("expected the parent's pillar block on the merged Component, got %#v", got.Spec.Schema)
	}
	if got.Spec.Schema.Kind == yaml.ScalarNode {
		t.Errorf("merged Spec.Schema must not still carry the inheritance marker")
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
	if !errors.Is(errs[0], ErrCyclicExtension()) {
		t.Errorf("expected ErrCyclicExtension, got %v", errs[0])
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
	if !errors.Is(errs[0], ErrInheritanceMerge()) {
		t.Errorf("expected ErrInheritanceMerge, got %v", errs[0])
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

// TestComponentAncestry_MultiLevelChain pins that the accessor reports every
// base in a chain, nearest parent first, and excludes the entity itself — the
// property a consumer asking "does this descend from that" relies on to answer
// through a grandparent, not merely one level up.
func TestComponentAncestry_MultiLevelChain(t *testing.T) {
	t.Parallel()
	root := types.Component{Metadata: types.ComponentMetadata{Name: "core/root", Version: "1.0.0"}}
	mid := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/mid", Version: "1.0.0"},
		Spec:     types.ComponentSpec{Extends: "core/root"},
	}
	leaf := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/leaf", Version: "1.0.0"},
		Spec:     types.ComponentSpec{Extends: "core/mid"},
	}
	lookup := func(uri string) (types.Component, bool) {
		switch uri {
		case "core/root":
			return root, true
		case "core/mid":
			return mid, true
		}
		return types.Component{}, false
	}

	got, errs := ComponentAncestry(leaf, lookup)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(got) != 2 || got[0].Metadata.Name != "core/mid" || got[1].Metadata.Name != "core/root" {
		t.Fatalf("expected [core/mid core/root] nearest-first; got %v", componentNames(got))
	}
}

// TestComponentAncestry_NoExtends pins the empty answer: an entity that declares
// no extends descends from nothing and is not itself part of its own ancestry.
func TestComponentAncestry_NoExtends(t *testing.T) {
	t.Parallel()
	solo := types.Component{Metadata: types.ComponentMetadata{Name: "app/solo", Version: "1.0.0"}}
	got, errs := ComponentAncestry(solo, func(string) (types.Component, bool) {
		t.Fatal("lookup must not be consulted for an entity with no extends")
		return types.Component{}, false
	})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty ancestry; got %v", componentNames(got))
	}
}

// TestComponentAncestry_CyclicChain pins that a circular chain terminates on the
// walk's visited set and reports ErrCyclicExtension. A consumer walking ancestry
// on untrusted content depends on this to report the chain fault rather than
// loop.
func TestComponentAncestry_CyclicChain(t *testing.T) {
	t.Parallel()
	a := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/a", Version: "1.0.0"},
		Spec:     types.ComponentSpec{Extends: "core/b"},
	}
	b := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/b", Version: "1.0.0"},
		Spec:     types.ComponentSpec{Extends: "core/a"},
	}
	lookup := func(uri string) (types.Component, bool) {
		switch uri {
		case "core/a":
			return a, true
		case "core/b":
			return b, true
		}
		return types.Component{}, false
	}

	got, errs := ComponentAncestry(a, lookup)
	if len(errs) == 0 {
		t.Fatalf("expected a cycle error; got ancestry %v", componentNames(got))
	}
	if !errors.Is(errs[0], ErrCyclicExtension()) {
		t.Errorf("expected ErrCyclicExtension; got %v", errs[0])
	}
	if got != nil {
		t.Errorf("a failed walk must return no ancestry; got %v", componentNames(got))
	}
}

// TestComponentAncestry_MissingParent pins the unresolvable-parent fault, which
// is distinct from a cycle and carries its own sentinel.
func TestComponentAncestry_MissingParent(t *testing.T) {
	t.Parallel()
	child := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/child", Version: "1.0.0"},
		Spec:     types.ComponentSpec{Extends: "core/absent"},
	}
	_, errs := ComponentAncestry(child, func(string) (types.Component, bool) {
		return types.Component{}, false
	})
	if len(errs) == 0 {
		t.Fatal("expected an unresolvable-parent error")
	}
	if !errors.Is(errs[0], ErrInheritanceMerge()) {
		t.Errorf("expected ErrInheritanceMerge; got %v", errs[0])
	}
}

func componentNames(comps []types.Component) []string {
	names := make([]string, 0, len(comps))
	for _, c := range comps {
		names = append(names, c.Metadata.Name)
	}
	return names
}

// omAssertions builds an OrderedMap of named assertions from name/body pairs,
// preserving author order.
func omAssertions(t *testing.T, pairs ...string) types.OrderedMap[string] {
	t.Helper()
	if len(pairs)%2 != 0 {
		t.Fatalf("omAssertions needs name/body pairs, got %d args", len(pairs))
	}
	out := types.OrderedMap[string]{}
	for i := 0; i < len(pairs); i += 2 {
		out.Set(pairs[i], pairs[i+1])
	}
	return out
}

// TestResolveComponentExtends_AssertionsAccumulateBaseFirst pins the additive
// merge and its order: the base's rules survive alongside the extending
// Component's own, and the base's come first, because evaluation halts on the
// first failure and a base's invariant is the more fundamental contract.
func TestResolveComponentExtends_AssertionsAccumulateBaseFirst(t *testing.T) {
	t.Parallel()
	base := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/base", Version: "1.0.0"},
		Spec: types.ComponentSpec{
			Assertions: omAssertions(t, "tls-required", `{{ if not .values.tls }}{{ fail "tls" }}{{ end }}`),
		},
	}
	child := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/gateway", Version: "1.0.0"},
		Spec: types.ComponentSpec{
			Extends:    "core/base",
			Assertions: omAssertions(t, "timeout-budget", `{{ if gt (int .values.t) 2000 }}{{ fail "t" }}{{ end }}`),
		},
	}
	lookup := func(uri string) (types.Component, bool) {
		if uri == "core/base" {
			return base, true
		}
		return types.Component{}, false
	}
	got, errs := ResolveComponentExtends(child, lookup)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if want := []string{"tls-required", "timeout-budget"}; !equalStrings(got.Spec.Assertions.Keys(), want) {
		t.Errorf("assertion order: got %v, want %v", got.Spec.Assertions.Keys(), want)
	}
	body, _ := got.Spec.Assertions.Get("tls-required")
	if body != `{{ if not .values.tls }}{{ fail "tls" }}{{ end }}` {
		t.Errorf("inherited assertion body altered: %q", body)
	}
}

// TestResolveComponentExtends_AssertionsAccumulateRootFirstOverChain pins the
// multi-level direction: a three-deep chain carries every level's rules with the
// chain root's first and the leaf's last.
func TestResolveComponentExtends_AssertionsAccumulateRootFirstOverChain(t *testing.T) {
	t.Parallel()
	root := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/root", Version: "1.0.0"},
		Spec:     types.ComponentSpec{Assertions: omAssertions(t, "root-rule", "R")},
	}
	mid := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/mid", Version: "1.0.0"},
		Spec: types.ComponentSpec{
			Extends:    "core/root",
			Assertions: omAssertions(t, "mid-rule", "M"),
		},
	}
	leaf := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/leaf", Version: "1.0.0"},
		Spec: types.ComponentSpec{
			Extends:    "core/mid",
			Assertions: omAssertions(t, "leaf-rule", "L"),
		},
	}
	lookup := func(uri string) (types.Component, bool) {
		switch uri {
		case "core/root":
			return root, true
		case "core/mid":
			return mid, true
		}
		return types.Component{}, false
	}
	got, errs := ResolveComponentExtends(leaf, lookup)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if want := []string{"root-rule", "mid-rule", "leaf-rule"}; !equalStrings(got.Spec.Assertions.Keys(), want) {
		t.Errorf("chain assertion order: got %v, want %v", got.Spec.Assertions.Keys(), want)
	}
}

// TestResolveComponentExtends_AssertionNameCollisionHalts pins the refusal: an
// extending Component reusing an inherited assertion name for a different rule
// is rejected rather than allowed to replace it, and the diagnostic names both
// entities and the colliding name so the author can rename without guessing.
func TestResolveComponentExtends_AssertionNameCollisionHalts(t *testing.T) {
	t.Parallel()
	base := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/base", Version: "1.0.0"},
		Spec:     types.ComponentSpec{Assertions: omAssertions(t, "tls-required", "BASE-RULE")},
	}
	child := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/gateway", Version: "1.0.0"},
		Spec: types.ComponentSpec{
			Extends:    "core/base",
			Assertions: omAssertions(t, "tls-required", "CHILD-RULE"),
		},
	}
	lookup := func(uri string) (types.Component, bool) {
		if uri == "core/base" {
			return base, true
		}
		return types.Component{}, false
	}
	got, errs := ResolveComponentExtends(child, lookup)
	if len(errs) != 1 {
		t.Fatalf("expected exactly one collision error, got %d: %v", len(errs), errs)
	}
	if !errors.Is(errs[0], ErrAssertionNameCollision()) {
		t.Errorf("expected ErrAssertionNameCollision, got %v", errs[0])
	}
	msg := errs[0].Error()
	for _, want := range []string{"core/gateway", "core/base", "tls-required", ".spec.assertions.tls-required"} {
		if !strings.Contains(msg, want) {
			t.Errorf("diagnostic missing %q: %s", want, msg)
		}
	}
	// A refused merge returns the entity unresolved, matching the chain-walk
	// failure paths, so no partially-merged entity reaches a consumer.
	if got.Spec.Assertions.Len() != 1 {
		t.Errorf("expected the unresolved child back, got %v", got.Spec.Assertions.Keys())
	}
	if body, _ := got.Spec.Assertions.Get("tls-required"); body != "CHILD-RULE" {
		t.Errorf("expected the unresolved child back, got body %q", body)
	}
}

// TestResolveComponentExtends_AssertionsIdempotentOnReMerge pins the property the
// catalog's resolution order depends on: resolving a chain whose middle tier has
// ALREADY had its own chain folded in must not report the middle tier's inherited
// rules as collisions against the base that declared them. Every other field's
// merge is idempotent by shape; this one is idempotent because a byte-identical
// body under a shared name is a restatement rather than a replacement.
func TestResolveComponentExtends_AssertionsIdempotentOnReMerge(t *testing.T) {
	t.Parallel()
	root := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/root", Version: "1.0.0"},
		Spec:     types.ComponentSpec{Assertions: omAssertions(t, "root-rule", "R")},
	}
	// midResolved is what the catalog holds after mid's own chain resolved: it
	// carries the root's rule as well as its own.
	midResolved := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/mid", Version: "1.0.0"},
		Spec: types.ComponentSpec{
			Extends:    "core/root",
			Assertions: omAssertions(t, "root-rule", "R", "mid-rule", "M"),
		},
	}
	leaf := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/leaf", Version: "1.0.0"},
		Spec: types.ComponentSpec{
			Extends:    "core/mid",
			Assertions: omAssertions(t, "leaf-rule", "L"),
		},
	}
	lookup := func(uri string) (types.Component, bool) {
		switch uri {
		case "core/root":
			return root, true
		case "core/mid":
			return midResolved, true
		}
		return types.Component{}, false
	}
	got, errs := ResolveComponentExtends(leaf, lookup)
	if len(errs) != 0 {
		t.Fatalf("re-merging an already-resolved ancestor must not collide: %v", errs)
	}
	if want := []string{"root-rule", "mid-rule", "leaf-rule"}; !equalStrings(got.Spec.Assertions.Keys(), want) {
		t.Errorf("re-merge order: got %v, want %v", got.Spec.Assertions.Keys(), want)
	}
}

// TestResolveFeatureExtends_AssertionsUnchanged pins that the Component-scoped
// additive merge did not reach the Feature surface: a Feature's assertions are
// still its own, with the base's not carried onto it.
func TestResolveFeatureExtends_AssertionsUnchanged(t *testing.T) {
	t.Parallel()
	base := types.Feature{
		Metadata: types.FeatureMetadata{Name: "core/f/base", Version: "1.0.0"},
		Spec:     types.FeatureSpec{Assertions: omAssertions(t, "base-rule", "B")},
	}
	child := types.Feature{
		Metadata: types.FeatureMetadata{Name: "core/f/child", Version: "1.0.0"},
		Spec: types.FeatureSpec{
			Extends:    "core/f/base",
			Assertions: omAssertions(t, "child-rule", "C"),
		},
	}
	lookup := func(uri string) (types.Feature, bool) {
		if uri == "core/f/base" {
			return base, true
		}
		return types.Feature{}, false
	}
	got, errs := ResolveFeatureExtends(child, lookup)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if want := []string{"child-rule"}; !equalStrings(got.Spec.Assertions.Keys(), want) {
		t.Errorf("Feature assertions changed: got %v, want %v", got.Spec.Assertions.Keys(), want)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestExtendsFaultsAreCatalogDomain pins the authoring domain every extends
// fault on a Component or a Feature reports. Both kinds are catalog content,
// so a broken extends chain is the catalog author's to fix — and the entity
// reference in the diagnostic names the kind, so a reader is told which
// catalog surface to open.
//
// The domain follows from the kind the failure was built with, which is why
// this holds for the chain-rebuild faults as well as the schema-merge ones:
// no site has to remember to prepend a word for the failure to be routed.
func TestExtendsFaultsAreCatalogDomain(t *testing.T) {
	t.Parallel()

	cyclicComponent := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/a", Version: "1.0.0"},
		Spec:     types.ComponentSpec{Extends: "core/b"},
	}
	componentLookup := func(uri string) (types.Component, bool) {
		switch uri {
		case "core/a":
			return cyclicComponent, true
		case "core/b":
			return types.Component{
				Metadata: types.ComponentMetadata{Name: "core/b", Version: "1.0.0"},
				Spec:     types.ComponentSpec{Extends: "core/a"},
			}, true
		}
		return types.Component{}, false
	}

	cyclicFeature := types.Feature{
		Metadata: types.FeatureMetadata{Name: "core/x", Version: "1.0.0"},
		Spec:     types.FeatureSpec{Extends: "core/y"},
	}
	featureLookup := func(uri string) (types.Feature, bool) {
		switch uri {
		case "core/x":
			return cyclicFeature, true
		case "core/y":
			return types.Feature{
				Metadata: types.FeatureMetadata{Name: "core/y", Version: "1.0.0"},
				Spec:     types.FeatureSpec{Extends: "core/x"},
			}, true
		}
		return types.Feature{}, false
	}

	cases := []struct {
		name      string
		resolve   func() []*ValidationError
		wantInMsg string
	}{
		{
			name: "component cycle",
			resolve: func() []*ValidationError {
				_, errs := ResolveComponentExtends(cyclicComponent, componentLookup)
				return errs
			},
			wantInMsg: "Component core/a",
		},
		{
			name: "component missing parent",
			resolve: func() []*ValidationError {
				child := types.Component{
					Metadata: types.ComponentMetadata{Name: "core/child", Version: "1.0.0"},
					Spec:     types.ComponentSpec{Extends: "core/vanished"},
				}
				_, errs := ResolveComponentExtends(child, func(string) (types.Component, bool) {
					return types.Component{}, false
				})
				return errs
			},
			wantInMsg: "Component core/child",
		},
		{
			name: "feature cycle",
			resolve: func() []*ValidationError {
				_, errs := ResolveFeatureExtends(cyclicFeature, featureLookup)
				return errs
			},
			wantInMsg: "Feature core/x",
		},
		{
			name: "feature missing parent",
			resolve: func() []*ValidationError {
				child := types.Feature{
					Metadata: types.FeatureMetadata{Name: "core/childfeat", Version: "1.0.0"},
					Spec:     types.FeatureSpec{Extends: "core/vanished"},
				}
				_, errs := ResolveFeatureExtends(child, func(string) (types.Feature, bool) {
					return types.Feature{}, false
				})
				return errs
			},
			wantInMsg: "Feature core/childfeat",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := tc.resolve()
			if len(errs) == 0 {
				t.Fatal("expected an extends fault")
			}
			ce := errs[0].ToCompilerError()
			if ce.Category != cerrors.CatalogError {
				t.Errorf("Category = %v, want %v", ce.Category, cerrors.CatalogError)
			}
			if !strings.Contains(ce.Message, tc.wantInMsg) {
				t.Errorf("Message should name the entity as %q; got %q", tc.wantInMsg, ce.Message)
			}
		})
	}
}
