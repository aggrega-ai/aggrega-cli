// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compiler_test — end-to-end proof that every resource of an assembled
// CloudFormation stack carries the build provenance, whichever input kind
// contributed it.
//
// The fixture's Recipe declares BOTH `source:` and `artifacts:`, so one node
// contributes a source-derived resource and two artifact-derived ones to the same
// Resources map. That pairing is what makes the assertions below a parity proof
// rather than a presence check: the resources are compared against each other in
// the document they share, so neither origin can pass by carrying a set the other
// does not.
//
// One of the artifact-derived resources is of a type that accepts no resource
// tags at all, and one artifact declares no resource. Both are deliberate: the
// first is what a tag-based placement would have skipped, and the second is the
// case where CloudFormation offers no site for provenance, which is emitted
// unchanged and named rather than passed through in silence.

package compiler_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

const (
	cfnProvenanceRelease = "testdata/cloudformation-provenance/release.yaml"
	cfnProvenanceCatalog = "testdata/cloudformation-provenance/catalog/base"

	// The Metadata key each resource carries its provenance under, written as the
	// literal an emitted stack carries rather than read from the placer's own
	// constant: the placer sits behind an import boundary this package cannot
	// cross, and what an output test should pin is the contract downstream readers
	// see in the bytes. The placer's constant is asserted against its own tests.
	cfnProvenanceMetadataKey = "aggrega.ai/provenance"

	// The source-derived resource, declared by the Recipe's sourced template.
	cfnProvenanceSourcedResource = "StoreBucket"
	// The artifact-derived resources. StoreQueuePolicy is the untaggable half:
	// AWS::SQS::QueuePolicy declares no Tags property, so a tag-based placement
	// would have had no site on it while its sibling queue carried one.
	cfnProvenanceArtifactResource   = "StoreQueue"
	cfnProvenanceUntaggableResource = "StoreQueuePolicy"
)

func cfnProvenanceInput() pipelinetest.Input {
	return pipelinetest.Input{ReleasePath: cfnProvenanceRelease, CatalogPaths: []string{cfnProvenanceCatalog}}
}

// cfnResourceProvenance reads the provenance block off one resource of a decoded
// stack template. It reports whether the resource carries one at all, so a caller
// distinguishes an absent block from an empty one.
func cfnResourceProvenance(t *testing.T, stack map[string]any, id string, raw []byte) (map[string]string, bool) {
	t.Helper()
	resources, ok := stack["Resources"].(map[string]any)
	if !ok {
		t.Fatalf("the assembled stack carries no Resources object\n--- stack ---\n%s", raw)
	}
	body, ok := resources[id].(map[string]any)
	if !ok {
		t.Fatalf("the assembled stack declares no resource %q; it declares %v\n--- stack ---\n%s", id, cfnSectionKeys(stack, "Resources"), raw)
	}
	metadata, ok := body["Metadata"].(map[string]any)
	if !ok {
		return nil, false
	}
	block, ok := metadata[cfnProvenanceMetadataKey].(map[string]any)
	if !ok {
		return nil, false
	}
	out := make(map[string]string, len(block))
	for k, v := range block {
		s, ok := v.(string)
		if !ok {
			t.Errorf("resource %q provenance key %q is not a string; got %#v", id, k, v)
			continue
		}
		out[k] = s
	}
	return out, true
}

// TestCloudFormationProvenance_EveryResourceCarriesTheSameSetWhicheverOriginSuppliedIt
// is the parity proof, asserted key by key in the emitted bytes.
//
// 🔴 The comparison is a set DIFFERENCE against the source-derived resource in the
// same document, naming whatever is missing or extra. A fixture checking that a
// provenance block merely exists would pass on a partial stamp, which is the state
// this test exists to distinguish from the fixed one. The values are compared too:
// every resource here comes from one node, so a value that differed would mean one
// placement path stamped a different identity — which no key-set comparison sees.
func TestCloudFormationProvenance_EveryResourceCarriesTheSameSetWhicheverOriginSuppliedIt(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, cfnProvenanceInput())
	stack, raw := findCfnStack(t, result.Artifacts, "stack")

	sourced, ok := cfnResourceProvenance(t, stack, cfnProvenanceSourcedResource, raw)
	if !ok {
		t.Fatalf("the source-derived resource %q carries no provenance block under %q\n--- stack ---\n%s",
			cfnProvenanceSourcedResource, cfnProvenanceMetadataKey, raw)
	}
	// 🔴 The reference side must be non-empty before anything is compared against
	// it, or every difference below is vacuously empty.
	if len(sourced) == 0 {
		t.Fatalf("the source-derived resource's provenance block is empty; the reference side of the parity comparison carries nothing\n--- stack ---\n%s", raw)
	}

	// Every resource the stack carries is checked, not a named pair: the promise is
	// about the document, so a resource added to the fixture later is covered
	// without anyone remembering to list it.
	ids := cfnSectionKeys(stack, "Resources")
	if !cfnContains(ids, cfnProvenanceArtifactResource) || !cfnContains(ids, cfnProvenanceUntaggableResource) {
		t.Fatalf("the artifact path must contribute %q and %q; the stack declares %v\n--- stack ---\n%s",
			cfnProvenanceArtifactResource, cfnProvenanceUntaggableResource, ids, raw)
	}
	for _, id := range ids {
		got, ok := cfnResourceProvenance(t, stack, id, raw)
		if !ok {
			t.Errorf("resource %q carries no provenance block under %q\n--- stack ---\n%s", id, cfnProvenanceMetadataKey, raw)
			continue
		}
		var missing, extra []string
		for k := range sourced {
			if _, present := got[k]; !present {
				missing = append(missing, k)
			}
		}
		for k := range got {
			if _, present := sourced[k]; !present {
				extra = append(extra, k)
			}
		}
		sort.Strings(missing)
		sort.Strings(extra)
		if len(missing) > 0 {
			t.Errorf("resource %q is missing provenance keys the source-derived resource carries: %v", id, missing)
		}
		if len(extra) > 0 {
			t.Errorf("resource %q carries provenance keys the source-derived resource does not: %v", id, extra)
		}
		for k, want := range sourced {
			if v := got[k]; v != want {
				t.Errorf("resource %q provenance key %q is %q; the source-derived resource carries %q", id, k, v, want)
			}
		}
	}

	// Two values spelled out, so the test states which identity is expected rather
	// than only that the resources agree: an identity from some other node would
	// satisfy a pure agreement check between two wrong values.
	if sourced["aggrega.ai/component-id"] != "store" {
		t.Errorf("provenance must carry the emitting node's identity; component-id = %q", sourced["aggrega.ai/component-id"])
	}
	if sourced["aggrega.ai/recipe-uri"] != "store-recipe:1.0.0" {
		t.Errorf("provenance must carry the producing Recipe's identity; recipe-uri = %q", sourced["aggrega.ai/recipe-uri"])
	}
	// The author-owned half of the label rail reaches this target too — the key
	// written literally on the Recipe and the key it promotes from the component.
	if sourced["hosting"] != "managed" {
		t.Errorf("a Recipe-declared output label must reach the stamped set; hosting = %q", sourced["hosting"])
	}
	if sourced["component.aggrega.ai/type"] != "resource" {
		t.Errorf("a promoted component label must reach the stamped set; component.aggrega.ai/type = %q", sourced["component.aggrega.ai/type"])
	}
}

// TestCloudFormationProvenance_UntaggableResourceTypeCarriesTheFullSet states
// separately what the parity proof above establishes incidentally, because it is
// the property that distinguishes this placement from the one the record named.
//
// AWS::SQS::QueuePolicy declares no Tags property. A tag-based stamp has no site
// on it, so such a resource would have been skipped — silently, since nothing
// downstream can tell a resource that refused a tag from one nobody tried to tag.
// The Metadata attribute is granted to every resource by the template language, so
// this resource carries the same set as the taggable one beside it.
func TestCloudFormationProvenance_UntaggableResourceTypeCarriesTheFullSet(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, cfnProvenanceInput())
	stack, raw := findCfnStack(t, result.Artifacts, "stack")

	resources, _ := stack["Resources"].(map[string]any)
	body, ok := resources[cfnProvenanceUntaggableResource].(map[string]any)
	if !ok {
		t.Fatalf("the stack declares no %q\n--- stack ---\n%s", cfnProvenanceUntaggableResource, raw)
	}
	// 🔴 The fixture's untaggable resource is identified by its declared type, so
	// this test cannot pass on a corpus quietly changed to an all-taggable set.
	if got := body["Type"]; got != "AWS::SQS::QueuePolicy" {
		t.Fatalf("%q is declared as %v; this test needs a resource type that accepts no tags", cfnProvenanceUntaggableResource, got)
	}
	if _, tagged := body["Properties"].(map[string]any)["Tags"]; tagged {
		t.Errorf("%q carries a Tags property; the type accepts none, so nothing may have added one", cfnProvenanceUntaggableResource)
	}
	got, ok := cfnResourceProvenance(t, stack, cfnProvenanceUntaggableResource, raw)
	if !ok || len(got) == 0 {
		t.Fatalf("the untaggable resource carries no provenance; a placement that only reached taggable types would leave it unstamped\n--- stack ---\n%s", raw)
	}
}

// TestCloudFormationProvenance_AuthoredContentIsAddedToNeverReplaced proves the
// stamp merges rather than overwrites, at both the levels a CloudFormation
// template carries a Metadata block.
//
// A resource's own Metadata is a place authors write to, so the provenance block
// has to land beside what they wrote. And the template-level Metadata section is a
// different thing entirely — a section the assembled stack carries under the names
// its contributors authored — so the provenance placement must leave it alone.
func TestCloudFormationProvenance_AuthoredContentIsAddedToNeverReplaced(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, cfnProvenanceInput())
	stack, raw := findCfnStack(t, result.Artifacts, "stack")

	resources, _ := stack["Resources"].(map[string]any)
	body, _ := resources[cfnProvenanceSourcedResource].(map[string]any)
	metadata, ok := body["Metadata"].(map[string]any)
	if !ok {
		t.Fatalf("resource %q carries no Metadata\n--- stack ---\n%s", cfnProvenanceSourcedResource, raw)
	}
	if got := metadata["StoreOwner"]; got != "platform" {
		t.Errorf("the author's own resource Metadata entry must survive the stamp; StoreOwner = %v\n--- stack ---\n%s", got, raw)
	}
	if _, present := metadata[cfnProvenanceMetadataKey]; !present {
		t.Errorf("the provenance block must sit beside the author's entries under the same Metadata attribute\n--- stack ---\n%s", raw)
	}

	// The template-level Metadata section, which is carried under the names its
	// author wrote and is not where provenance lands.
	if got := cfnSectionKeys(stack, "Metadata"); !cfnContains(got, "StoreDocumentation") {
		t.Errorf("the authored template-level Metadata section must still be carried; Metadata = %v\n--- stack ---\n%s", got, raw)
	}
	if got := cfnSectionKeys(stack, "Metadata"); cfnContains(got, cfnProvenanceMetadataKey) {
		t.Errorf("provenance must not reach the template-level Metadata section, which speaks for the whole template rather than for one contributor; Metadata = %v", got)
	}
}

// TestCloudFormationProvenance_NoKubernetesOnlyKeyReachesTheStack states the
// difference between this target's key set and a Kubernetes-shaped provider's.
//
// The shared builders produce two keys that only a Kubernetes runtime consumes —
// the recommended-label name and a GitOps controller's ordering annotation.
// Neither names anything in a CloudFormation stack, so neither is carried. The
// plain provenance sync-wave entry is a different key and is retained wherever the
// node declares a wave.
func TestCloudFormationProvenance_NoKubernetesOnlyKeyReachesTheStack(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, cfnProvenanceInput())
	_, raw := findCfnStack(t, result.Artifacts, "stack")

	for _, key := range []string{"app.kubernetes.io/name", "argocd.argoproj.io/sync-wave"} {
		if strings.Contains(string(raw), key) {
			t.Errorf("the emitted stack carries %q, which only a Kubernetes runtime consumes\n--- stack ---\n%s", key, raw)
		}
	}
}

// TestCloudFormationProvenance_ResourcelessFragmentReachesTheStackUnchanged is the
// pass-through half of the placer's two outcomes. A fragment declaring no resource
// has no site for provenance; it must still reach the assembled stack with its own
// content intact rather than being dropped or rewritten. The advisory naming it is
// asserted where the placer produces it.
func TestCloudFormationProvenance_ResourcelessFragmentReachesTheStackUnchanged(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, cfnProvenanceInput())
	stack, raw := findCfnStack(t, result.Artifacts, "stack")

	if got := cfnSectionKeys(stack, "Parameters"); !cfnContains(got, "StoreRetentionDays") {
		t.Errorf("the resource-less fragment's Parameters must still reach the assembled stack; Parameters = %v\n--- stack ---\n%s", got, raw)
	}
}

// TestCloudFormationProvenance_Deterministic asserts the stamp does not cost the
// Deterministic Output invariant: provenance carries a compiler version and a
// sorted key set, so a non-deterministic stamp would break byte-identity across
// runs of identical input.
func TestCloudFormationProvenance_Deterministic(t *testing.T) {
	pipelinetest.AssertDeterministic(t, cfnProvenanceInput())
}
