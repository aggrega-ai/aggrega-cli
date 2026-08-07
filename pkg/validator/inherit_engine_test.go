// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

func TestResolveInheritance_SingleLevel(t *testing.T) {
	child := CatalogEntry{
		Kind:     envelope.KindComponent,
		URI:      "child:1.0.0",
		Extends:  "parent:1.0.0",
		Metadata: map[string]any{"name": "child", "extra": "from-child"},
	}

	lookup := func(uri string) (CatalogEntry, bool) {
		if uri == "parent:1.0.0" {
			return CatalogEntry{
				Kind:     envelope.KindComponent,
				URI:      "parent:1.0.0",
				Metadata: map[string]any{"name": "parent", "base_key": "from-parent"},
			}, true
		}
		return CatalogEntry{}, false
	}

	result := ResolveInheritance(child, lookup)
	if len(result.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", result.Errors[0].Error())
	}
	if result.MergedMetadata["name"] != "child" {
		t.Errorf("child name should win, got %v", result.MergedMetadata["name"])
	}
	if result.MergedMetadata["base_key"] != "from-parent" {
		t.Errorf("parent key should be inherited, got %v", result.MergedMetadata["base_key"])
	}
}

func TestResolveInheritance_MultiLevelChain(t *testing.T) {
	grandchild := CatalogEntry{
		Kind:     envelope.KindComponent,
		URI:      "grandchild:1.0.0",
		Extends:  "child:1.0.0",
		Metadata: map[string]any{"level": "3"},
	}

	lookup := func(uri string) (CatalogEntry, bool) {
		switch uri {
		case "child:1.0.0":
			return CatalogEntry{
				Kind:     envelope.KindComponent,
				URI:      "child:1.0.0",
				Extends:  "parent:1.0.0",
				Metadata: map[string]any{"level": "2", "child_key": "c"},
			}, true
		case "parent:1.0.0":
			return CatalogEntry{
				Kind:     envelope.KindComponent,
				URI:      "parent:1.0.0",
				Metadata: map[string]any{"level": "1", "parent_key": "p"},
			}, true
		}
		return CatalogEntry{}, false
	}

	result := ResolveInheritance(grandchild, lookup)
	if len(result.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", result.Errors[0].Error())
	}
	if result.MergedMetadata["level"] != "3" {
		t.Errorf("grandchild level should win, got %v", result.MergedMetadata["level"])
	}
	if result.MergedMetadata["parent_key"] != "p" {
		t.Errorf("parent_key should be inherited, got %v", result.MergedMetadata["parent_key"])
	}
	if result.MergedMetadata["child_key"] != "c" {
		t.Errorf("child_key should be inherited, got %v", result.MergedMetadata["child_key"])
	}
}

func TestResolveInheritance_CycleDetection(t *testing.T) {
	entry := CatalogEntry{
		Kind:    envelope.KindComponent,
		URI:     "a:1.0.0",
		Extends: "b:1.0.0",
	}

	lookup := func(uri string) (CatalogEntry, bool) {
		switch uri {
		case "b:1.0.0":
			return CatalogEntry{Kind: envelope.KindComponent, URI: "b:1.0.0", Extends: "a:1.0.0"}, true
		}
		return CatalogEntry{}, false
	}

	result := ResolveInheritance(entry, lookup)
	if len(result.Errors) == 0 {
		t.Fatal("expected cycle detection error")
	}
	if !errors.Is(result.Errors[0], ErrCyclicExtension) {
		t.Errorf("expected ErrCyclicExtension, got %v", result.Errors[0])
	}
	if !strings.Contains(result.Errors[0].Error(), " — ") {
		t.Errorf("expected the diagnostic to carry a constraint clause; got: %s", result.Errors[0].Error())
	}
}

func TestResolveInheritance_ParentNotFound(t *testing.T) {
	entry := CatalogEntry{
		Kind:    envelope.KindComponent,
		URI:     "child:1.0.0",
		Extends: "nonexistent:1.0.0",
	}

	lookup := func(uri string) (CatalogEntry, bool) {
		return CatalogEntry{}, false
	}

	result := ResolveInheritance(entry, lookup)
	if len(result.Errors) == 0 {
		t.Fatal("expected error for missing parent")
	}
	if !errors.Is(result.Errors[0], ErrInheritanceMerge) {
		t.Errorf("expected ErrInheritanceMerge, got %v", result.Errors[0])
	}
}

func TestResolveInheritance_NoExtends(t *testing.T) {
	entry := CatalogEntry{
		Kind:     envelope.KindComponent,
		URI:      "standalone:1.0.0",
		Metadata: map[string]any{"name": "standalone"},
	}

	result := ResolveInheritance(entry, nil)
	if len(result.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", result.Errors[0].Error())
	}
	if result.MergedMetadata["name"] != "standalone" {
		t.Errorf("expected standalone metadata, got %v", result.MergedMetadata["name"])
	}
}
