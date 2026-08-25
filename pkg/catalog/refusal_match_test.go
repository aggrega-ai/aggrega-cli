// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"testing"
)

// refusal builds one entry of a tolerant load's record with the identity the
// refused document declared for itself.
func refusal(path, kind, name, version, capability string) SkippedEntity {
	return SkippedEntity{
		Path:       path,
		Kind:       kind,
		Name:       name,
		Version:    version,
		Capability: capability,
		Err:        errors.New("refused at load"),
	}
}

// TestSoleRefusalFor_MatchesOnlyTheDeclaredIdentity pins the match key: every
// part of the identity a reference states has to agree with the identity the
// refused document declares. Each case below removes exactly one agreement, so a
// condition dropped from the match makes that case — and only that case — report
// a match it must not.
func TestSoleRefusalFor_MatchesOnlyTheDeclaredIdentity(t *testing.T) {
	t.Parallel()

	helmServer := refusal("/cat/recipes/helm/server.yaml", "Recipe", "server-recipe", "1.0.0", "helm")

	for _, tc := range []struct {
		name     string
		skipped  []SkippedEntity
		uri      string
		kindHint string
		wantPath string // empty means: no match
	}{
		{
			name:     "a bare reference matches the one refusal declaring that name and version",
			skipped:  []SkippedEntity{helmServer},
			uri:      "server-recipe:1.0.0",
			kindHint: "recipe",
			wantPath: "/cat/recipes/helm/server.yaml",
		},
		{
			name:     "a provider-qualified reference matches when the declared capability agrees",
			skipped:  []SkippedEntity{helmServer},
			uri:      "helm/server-recipe:1.0.0",
			kindHint: "recipe",
			wantPath: "/cat/recipes/helm/server.yaml",
		},
		{
			name:     "a provider-qualified reference does NOT match a different declared capability",
			skipped:  []SkippedEntity{helmServer},
			uri:      "compose/server-recipe:1.0.0",
			kindHint: "recipe",
			wantPath: "",
		},
		{
			name:     "a version the refusal does not declare does NOT match",
			skipped:  []SkippedEntity{helmServer},
			uri:      "server-recipe:2.0.0",
			kindHint: "recipe",
			wantPath: "",
		},
		{
			name:     "a version RANGE names no single document, so it matches nothing",
			skipped:  []SkippedEntity{helmServer},
			uri:      "server-recipe:^1.0.0",
			kindHint: "recipe",
			wantPath: "",
		},
		{
			name:     "a reference of another kind does NOT match",
			skipped:  []SkippedEntity{helmServer},
			uri:      "server-recipe:1.0.0",
			kindHint: "component",
			wantPath: "",
		},
		{
			name:     "a name no refusal declares matches nothing",
			skipped:  []SkippedEntity{helmServer},
			uri:      "ghost-recipe:1.0.0",
			kindHint: "recipe",
			wantPath: "",
		},
		{
			name: "TWO refusals declaring one name and version are ambiguous, so neither is named",
			skipped: []SkippedEntity{
				helmServer,
				refusal("/cat/recipes/compose/server.yaml", "Recipe", "server-recipe", "1.0.0", "compose"),
			},
			uri:      "server-recipe:1.0.0",
			kindHint: "recipe",
			wantPath: "",
		},
		{
			name: "the same two are distinguishable once the reference states the provider",
			skipped: []SkippedEntity{
				helmServer,
				refusal("/cat/recipes/compose/server.yaml", "Recipe", "server-recipe", "1.0.0", "compose"),
			},
			uri:      "compose/server-recipe:1.0.0",
			kindHint: "recipe",
			wantPath: "/cat/recipes/compose/server.yaml",
		},
		{
			name:     "a refusal whose header yielded no identity is never named",
			skipped:  []SkippedEntity{refusal("/cat/recipes/helm/server.yaml", "", "", "", "")},
			uri:      "server-recipe:1.0.0",
			kindHint: "recipe",
			wantPath: "",
		},
		{
			name:     "a refusal that declares a kind but no name is never named",
			skipped:  []SkippedEntity{refusal("/cat/recipes/helm/server.yaml", "Recipe", "", "", "helm")},
			uri:      "server-recipe:1.0.0",
			kindHint: "recipe",
			wantPath: "",
		},
		{
			name:     "a kind hint that maps to no catalog kind matches nothing",
			skipped:  []SkippedEntity{helmServer},
			uri:      "server-recipe:1.0.0",
			kindHint: "",
			wantPath: "",
		},
		{
			name:     "an unversioned reference matches the one refusal of that name",
			skipped:  []SkippedEntity{helmServer},
			uri:      "server-recipe",
			kindHint: "recipe",
			wantPath: "/cat/recipes/helm/server.yaml",
		},
		{
			name: "an unversioned reference is ambiguous across two versions of one name",
			skipped: []SkippedEntity{
				helmServer,
				refusal("/cat/recipes/helm/server-2.yaml", "Recipe", "server-recipe", "2.0.0", "helm"),
			},
			uri:      "server-recipe",
			kindHint: "recipe",
			wantPath: "",
		},
		{
			name:     "a component name carrying a slash is NOT split into a provider segment",
			skipped:  []SkippedEntity{refusal("/cat/base/components/server.yaml", "Component", "core/server", "1.0.0", "")},
			uri:      "core/server:1.0.0",
			kindHint: "component",
			wantPath: "/cat/base/components/server.yaml",
		},
		{
			name:     "an empty record matches nothing",
			skipped:  nil,
			uri:      "server-recipe:1.0.0",
			kindHint: "recipe",
			wantPath: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := SoleRefusalFor(tc.skipped, tc.uri, tc.kindHint)
			if tc.wantPath == "" {
				if ok {
					t.Fatalf("SoleRefusalFor(%q, %q) named %q; the record cannot single one out, so it must decline",
						tc.uri, tc.kindHint, got.Path)
				}
				return
			}
			if !ok {
				t.Fatalf("SoleRefusalFor(%q, %q) declined; want %q", tc.uri, tc.kindHint, tc.wantPath)
			}
			if got.Path != tc.wantPath {
				t.Errorf("SoleRefusalFor(%q, %q) named %q; want %q", tc.uri, tc.kindHint, got.Path, tc.wantPath)
			}
		})
	}
}

// TestNewSkippedEntity_RecoversTheWholeDeclaredIdentity proves the record the
// match reads is actually populated. Kind and name alone cannot tell two refused
// recipes of one name apart, so the version and the capability have to be
// recovered too — and a document too malformed to peek yields none of them,
// which is what keeps it out of every candidate set.
func TestNewSkippedEntity_RecoversTheWholeDeclaredIdentity(t *testing.T) {
	t.Parallel()

	doc := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server-recipe
  version: "1.0.0"
spec:
  capability: helm
`)
	se := newSkippedEntity("/cat/recipes/helm/server.yaml", doc, errors.New("refused"))
	if se.Kind != "Recipe" || se.Name != "server-recipe" || se.Version != "1.0.0" || se.Capability != "helm" {
		t.Errorf("recovered identity = %+v; want Recipe/server-recipe/1.0.0/helm", se)
	}

	// An unreadable file is recorded with no bytes at all, which is the shape a
	// document nothing could peek at reaches this function in.
	blank := newSkippedEntity("/cat/recipes/helm/unreadable.yaml", nil, errors.New("refused"))
	if blank.Kind != "" || blank.Name != "" || blank.Version != "" || blank.Capability != "" {
		t.Errorf("identity recovered from no bytes = %+v; want every field empty", blank)
	}
	if _, ok := SoleRefusalFor([]SkippedEntity{blank}, "server-recipe:1.0.0", "recipe"); ok {
		t.Error("a refusal with no declared identity must never be matched to a reference")
	}
}
