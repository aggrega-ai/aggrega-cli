// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestDescribeToolResolvesEveryKindTheResolutionReaches proves this surface
// answers the same by-name question the other surfaces answer.
//
// The kinds this tool reaches are not a set of its own: it runs the shared
// resolution, so a name that resolves to a Recipe at a command line resolves to a
// Recipe here. A surface reaching a narrower set than its neighbours tells a
// caller that an entity does not exist when the caller has simply moved to a
// different surface, which is indistinguishable from a mistyped name.
func TestDescribeToolResolvesEveryKindTheResolutionReaches(t *testing.T) {
	s := newTestServer(t)
	root, catalogDir, _ := fullstackWorkspace(t)

	cases := map[string]struct{ name, kind string }{
		"component":     {"core/compute/api-server", "Component"},
		"feature":       {"feature.myco/db-pool", "Feature"},
		"recipe":        {"helm-api-server-synthesized", "Recipe"},
		"featureRecipe": {"db-pool-patcher", "FeatureRecipe"},
		"plugin":        {"provider/helm", "Plugin"},
		"aggregate":     {"db-module", "Aggregate"},
	}
	for label, tc := range cases {
		res := callTool(t, s, "describe", describeArgs{
			Name: tc.name, WorkspaceRoot: root, CatalogDirs: []string{catalogDir},
		})
		if res.IsError {
			t.Errorf("%s: describe %q reported an error: %s", label, tc.name, res.Content[0].Text)
			continue
		}
		var payload describeEntityPayload
		if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
			t.Errorf("%s: unmarshal payload: %v", label, err)
			continue
		}
		if payload.Kind != tc.kind {
			t.Errorf("%s: %q described as kind %q, want %q", label, tc.name, payload.Kind, tc.kind)
		}
		if len(payload.Definition) == 0 {
			t.Errorf("%s: %q described with an empty definition", label, tc.name)
		}
	}
}

// TestDescribeToolRefusesAnUnresolvedName pins the refusal, with a positive
// control on the same workspace so the refusal cannot be a workspace that
// resolves nothing at all.
//
// A name that resolves to nothing is refused rather than answered with an empty
// definition, because an empty answer reports a mistyped name as an entity with
// nothing to say about itself.
func TestDescribeToolRefusesAnUnresolvedName(t *testing.T) {
	s := newTestServer(t)
	root, catalogDir, _ := fullstackWorkspace(t)

	ok := callTool(t, s, "describe", describeArgs{
		Name: "core/compute/api-server", WorkspaceRoot: root, CatalogDirs: []string{catalogDir},
	})
	if ok.IsError {
		t.Fatalf("positive control: a name this workspace declares was refused (%s), so the refusal below proves nothing",
			ok.Content[0].Text)
	}

	miss := callTool(t, s, "describe", describeArgs{
		Name: "core/compute/api-servr", WorkspaceRoot: root, CatalogDirs: []string{catalogDir},
	})
	if !miss.IsError {
		t.Fatalf("a name that resolves to nothing was answered: %s", string(miss.StructuredContent))
	}
	text := miss.Content[0].Text
	if !strings.Contains(text, "core/compute/api-servr") {
		t.Errorf("the refusal does not name what was asked for; got: %s", text)
	}
	if !strings.Contains(text, "in any kind") {
		t.Errorf("the refusal does not say the whole resolution was tried; got: %s", text)
	}
}

// TestDescribeToolReportsClosureKindsAsAnAuthorWritesThem pins that the kind on a
// closure entry is the word an author types at the head of a document, not the
// note the walk keeps for itself — the same rendering every other surface
// reporting a closure applies, so one entry reads the same wherever it is read.
func TestDescribeToolReportsClosureKindsAsAnAuthorWritesThem(t *testing.T) {
	s := newTestServer(t)
	root, catalogDir, releasePath := fullstackWorkspace(t)

	res := callTool(t, s, "describe", describeArgs{
		Release: releasePath, WorkspaceRoot: root, CatalogDirs: []string{catalogDir},
	})
	if res.IsError {
		t.Fatalf("describe release reported an error: %s", res.Content[0].Text)
	}
	var payload describeReleasePayload
	if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if len(payload.Entities) == 0 {
		t.Fatal("the closure carried no entities, so the kinds below would be read off an empty list")
	}
	var kinded int
	for _, e := range payload.Entities {
		if e.Kind == "" {
			continue
		}
		kinded++
		switch e.Kind {
		case "Recipe", "FeatureRecipe", "Aggregate":
		default:
			t.Errorf("entry %q carries kind %q, which is not a kind an author writes", e.URI, e.Kind)
		}
	}
	if kinded == 0 {
		t.Fatal("no entry carried a kind at all, so this asserts nothing about how kinds are rendered")
	}
}
