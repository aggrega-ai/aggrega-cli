// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"errors"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestResolveComponentExtends_NoExtends(t *testing.T) {
	t.Parallel()
	comp := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/standalone", Version: "1.0.0"},
		Spec:     types.ComponentSpec{CueSchema: `values: {port: int}`},
	}
	got, errs := ResolveComponentExtends(comp, func(string) (types.Component, bool) { return types.Component{}, false })
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if got.Spec.CueSchema != comp.Spec.CueSchema {
		t.Errorf("no-extends should return unchanged comp")
	}
}

func TestResolveComponentExtends_MergesLabelsAndSecretNames(t *testing.T) {
	t.Parallel()
	labels := types.OrderedMap[string]{}
	labels.Set(types.ComponentTypeLabel, "server")
	labels.Set("tier", "base")
	usesSchema := types.OrderedMap[types.UsesSchemaEntry]{}
	usesSchema.Set("databases", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=stateful-server"})
	parent := types.Component{
		Metadata: types.ComponentMetadata{
			Name:    "core/server/base",
			Version: "1.0.0",
			Labels:  labels,
		},
		Spec: types.ComponentSpec{
			SecretNames: []string{"root_password"},
			Inject:      []string{"env"},
			UsesSchema:  usesSchema,
			CueSchema:   `values: { port?: int & >=1024 }`,
		},
	}
	labels2 := types.OrderedMap[string]{}
	labels2.Set("tier", "prod")
	usesSchema2 := types.OrderedMap[types.UsesSchemaEntry]{}
	usesSchema2.Set("caches", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=resource"})
	child := types.Component{
		Metadata: types.ComponentMetadata{
			Name:    "core/server/api",
			Version: "1.0.0",
			Labels:  labels2, // overrides parent tier=base
		},
		Spec: types.ComponentSpec{
			Extends:     "core/server/base",
			SecretNames: []string{"api_token"},
			Inject:      []string{"env", "file"},
			UsesSchema:  usesSchema2,
			CueSchema:   `values: { name: string }`,
		},
	}

	lookup := func(uri string) (types.Component, bool) {
		if uri == "core/server/base" {
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

	// Concatenated + deduped secret names
	if len(got.Spec.SecretNames) != 2 {
		t.Errorf("expected 2 secretNames, got %v", got.Spec.SecretNames)
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

	// CueSchema concatenates parent + child sources so constraints survive
	if !strings.Contains(got.Spec.CueSchema, ">=1024") {
		t.Errorf("expected parent constraint in unified CUE, got:\n%s", got.Spec.CueSchema)
	}
	if !strings.Contains(got.Spec.CueSchema, "name: string") {
		t.Errorf("expected child field in unified CUE, got:\n%s", got.Spec.CueSchema)
	}

	// Identity stays child's
	if got.Metadata.Name != "core/server/api" {
		t.Errorf("identity should be child's name, got %q", got.Metadata.Name)
	}
}

func TestResolveComponentExtends_MergesSecretsSchema(t *testing.T) {
	t.Parallel()
	secretsSchema := types.OrderedMap[types.SecretSlot]{}
	secretsSchema.Set("root_password", types.SecretSlot{Required: true, Inject: []string{"env"}})
	secretsSchema.Set("tls_ca", types.SecretSlot{Required: false})
	parent := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/db/base", Version: "1.0.0"},
		Spec: types.ComponentSpec{
			SecretsSchema: secretsSchema,
		},
	}
	secretsSchema2 := types.OrderedMap[types.SecretSlot]{}
	secretsSchema2.Set("root_password", types.SecretSlot{Required: true, Inject: []string{"env"}, Description: "strict"})
	secretsSchema2.Set("api_token", types.SecretSlot{Required: true})
	child := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/db/strict", Version: "1.0.0"},
		Spec: types.ComponentSpec{
			Extends:       "core/db/base",
			SecretsSchema: secretsSchema2,
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
	rootSlot, _ := got.Spec.SecretsSchema.Get("root_password")
	if rootSlot.Description != "strict" {
		t.Errorf("child slot should override parent's, got %v", rootSlot)
	}
	if _, ok := got.Spec.SecretsSchema.Get("tls_ca"); !ok {
		t.Error("parent-only slot lost during merge")
	}
	if _, ok := got.Spec.SecretsSchema.Get("api_token"); !ok {
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
			CueSchema: `values: { scrape_port: int }`,
		},
	}
	child := types.Feature{
		Metadata: types.FeatureMetadata{Name: "core/monitoring/strict", Version: "1.0.0"},
		Spec: types.FeatureSpec{
			Extends:   "core/monitoring/base",
			CueSchema: `values: { scrape_port: int & >=9000 }`,
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
	if !strings.Contains(got.Spec.CueSchema, ">=9000") {
		t.Errorf("child constraint missing in unified schema: %s", got.Spec.CueSchema)
	}
}
