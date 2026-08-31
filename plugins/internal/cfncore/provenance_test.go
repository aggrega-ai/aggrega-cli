// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cfncore

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
)

// provenanceFixtureNodeCtx is a node context populating every field the shared
// provenance builders read, so a key the builders gain shows up in the assertions
// below rather than eliding on an unset field.
func provenanceFixtureNodeCtx() rendercore.NodeRenderContext {
	return rendercore.NodeRenderContext{
		NodeID:          "store",
		BoundaryPath:    "cluster/stack",
		SyncWave:        -1,
		RecipeName:      "store-recipe",
		RecipeVersion:   "1.0.0",
		ProviderName:    "cloudformation",
		ProviderVersion: "0.5.0",
		OutputLabels:    map[string]string{"hosting": "managed"},
		Provenance: rendercore.ProvenanceContext{
			CompilerVersion: "9.9.9-fixture",
			AggregateName:   "store-app",
			Environment:     "production",
		},
	}
}

func sortedKeysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestProvenanceSet_IsTheSharedUnionMinusTheKubernetesOnlyKeys is the no-silent-gap
// assertion: the set this target carries is COMPUTED from the shared builders
// rather than listed here, so a key added to either builder reaches CloudFormation
// automatically and a key this target silently failed to pick up fails the test.
//
// A hand-written expected list would instead go quietly stale the moment the
// provenance set grew — passing while the target lagged, which is exactly the
// condition it would exist to catch.
func TestProvenanceSet_IsTheSharedUnionMinusTheKubernetesOnlyKeys(t *testing.T) {
	nodeCtx := provenanceFixtureNodeCtx()
	labels := rendercore.BuildAggregaProvenanceLabels(nodeCtx)
	annotations := rendercore.BuildAggregaProvenanceAnnotations(nodeCtx, rendercore.ProvenanceRecipeURIFromNodeCtx(nodeCtx))

	want := map[string]string{}
	for k, v := range labels {
		want[k] = v
	}
	for k, v := range annotations {
		want[k] = v
	}
	for _, k := range rendercore.KubernetesOnlyProvenanceKeys {
		delete(want, k)
	}

	got := ProvenanceSetFromNodeContext(nodeCtx)
	if len(got) != len(want) {
		t.Fatalf("the stamped set has %d keys, want %d\n got: %v\nwant: %v", len(got), len(want), sortedKeysOf(got), sortedKeysOf(want))
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("key %q is %q, want %q", k, got[k], v)
		}
	}

	// The two Kubernetes-only keys are asserted absent by name as well, so the
	// computation above cannot pass by having dropped nothing: the fixture context
	// populates both, so both are present in the union it is derived from.
	for _, k := range rendercore.KubernetesOnlyProvenanceKeys {
		if _, present := labels[k]; !present {
			if _, present := annotations[k]; !present {
				t.Fatalf("the fixture context does not produce %q, so its absence from the stamped set proves nothing", k)
			}
		}
		if _, present := got[k]; present {
			t.Errorf("the stamped set carries %q, which only a Kubernetes runtime consumes", k)
		}
	}
	// The provenance wave entry is a different key and is retained.
	if got["aggrega.ai/sync-wave"] != "-1" {
		t.Errorf("the provenance sync-wave entry must be retained; got %q", got["aggrega.ai/sync-wave"])
	}
}

// TestStampResourceProvenance_StampsEveryResourceAndPreservesAuthoredMetadata
// covers the ordinary case: the block lands on each resource under the reserved
// key, an author's own Metadata entries survive beside it, and nothing else in the
// document moves.
func TestStampResourceProvenance_StampsEveryResourceAndPreservesAuthoredMetadata(t *testing.T) {
	in := []byte(`{
	  "Resources": {
	    "Plain": {"Type": "AWS::SQS::Queue"},
	    "Documented": {"Type": "AWS::S3::Bucket", "Metadata": {"Owner": "platform"}}
	  },
	  "Parameters": {"Size": {"Type": "String"}}
	}`)
	provenance := map[string]string{"aggrega.ai/component-id": "store", "hosting": "managed"}

	body, warning, err := StampResourceProvenance("template.json", in, provenance)
	if err != nil {
		t.Fatalf("stamp: %v", err)
	}
	if warning != "" {
		t.Errorf("a template declaring resources must stamp without an advisory; got %q", warning)
	}

	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("stamped body is not valid JSON: %v\n%s", err, body)
	}
	resources, ok := doc["Resources"].(map[string]any)
	if !ok {
		t.Fatalf("stamped body carries no Resources\n%s", body)
	}
	for _, id := range []string{"Plain", "Documented"} {
		resource, ok := resources[id].(map[string]any)
		if !ok {
			t.Fatalf("resource %q missing from the stamped body\n%s", id, body)
		}
		metadata, ok := resource["Metadata"].(map[string]any)
		if !ok {
			t.Fatalf("resource %q carries no Metadata\n%s", id, body)
		}
		block, ok := metadata[ProvenanceMetadataKey].(map[string]any)
		if !ok {
			t.Fatalf("resource %q carries no provenance block under %q\n%s", id, ProvenanceMetadataKey, body)
		}
		if len(block) != len(provenance) {
			t.Errorf("resource %q provenance block has %d keys, want %d: %#v", id, len(block), len(provenance), block)
		}
		for k, want := range provenance {
			if block[k] != want {
				t.Errorf("resource %q provenance key %q is %#v, want %q", id, k, block[k], want)
			}
		}
	}
	if got := resources["Documented"].(map[string]any)["Metadata"].(map[string]any)["Owner"]; got != "platform" {
		t.Errorf("the author's own Metadata entry must survive; Owner = %#v", got)
	}
	if _, present := doc["Parameters"]; !present {
		t.Errorf("a section the stamp does not touch must survive\n%s", body)
	}
}

// TestStampResourceProvenance_ResourcelessTemplateIsNamedNotSilent covers the
// other outcome. A template declaring no resource has no site for provenance; the
// bytes go back unchanged AND the advisory names the template, so a caller never
// receives "unstamped" and "nothing to report" together.
func TestStampResourceProvenance_ResourcelessTemplateIsNamedNotSilent(t *testing.T) {
	in := []byte(`{"Parameters": {"Size": {"Type": "String"}}}`)

	body, warning, err := StampResourceProvenance("parameters.json", in, map[string]string{"aggrega.ai/component-id": "store"})
	if err != nil {
		t.Fatalf("stamp: %v", err)
	}
	if string(body) != string(in) {
		t.Errorf("a template with no site for provenance must be emitted unchanged; got\n%s", body)
	}
	if warning == "" {
		t.Fatal("a template with no site for provenance must be named; a silent skip is indistinguishable from a missed stamp")
	}
	for _, want := range []string{"parameters.json", "Metadata"} {
		if !strings.Contains(warning, want) {
			t.Errorf("the advisory does not mention %q: %q", want, warning)
		}
	}
}

// TestStampResourceProvenance_UnreachableShapeIsNamedNotRewritten covers the
// shapes the placer has no reading of: a resource whose body, or whose Metadata
// attribute, is not a mapping. Such a resource is emitted exactly as authored and
// named in the advisory — the compiler does not rewrite authored content into a
// shape it finds convenient — while its well-formed siblings still stamp.
func TestStampResourceProvenance_UnreachableShapeIsNamedNotRewritten(t *testing.T) {
	in := []byte(`{
	  "Resources": {
	    "Fine": {"Type": "AWS::SQS::Queue"},
	    "ScalarBody": "not a mapping",
	    "ScalarMetadata": {"Type": "AWS::S3::Bucket", "Metadata": "not a mapping"}
	  }
	}`)

	body, warning, err := StampResourceProvenance("template.json", in, map[string]string{"aggrega.ai/component-id": "store"})
	if err != nil {
		t.Fatalf("stamp: %v", err)
	}
	if warning == "" {
		t.Fatal("a resource the stamp cannot reach must be named")
	}
	for _, want := range []string{"ScalarBody", "ScalarMetadata", "template.json"} {
		if !strings.Contains(warning, want) {
			t.Errorf("the advisory does not name %q: %q", want, warning)
		}
	}
	if strings.Contains(warning, "\"Fine\"") {
		t.Errorf("the advisory names a resource that stamped cleanly: %q", warning)
	}

	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("stamped body is not valid JSON: %v\n%s", err, body)
	}
	resources := doc["Resources"].(map[string]any)
	if got := resources["ScalarBody"]; got != "not a mapping" {
		t.Errorf("an unreachable resource body must be emitted as authored; got %#v", got)
	}
	if got := resources["ScalarMetadata"].(map[string]any)["Metadata"]; got != "not a mapping" {
		t.Errorf("an unreachable Metadata attribute must be emitted as authored; got %#v", got)
	}
	if _, ok := resources["Fine"].(map[string]any)["Metadata"].(map[string]any)[ProvenanceMetadataKey]; !ok {
		t.Errorf("a well-formed sibling must still stamp\n%s", body)
	}
}

// TestStampResourceProvenance_EmptySetStampsNothingAndReportsNothing separates the
// two conditions a caller must not conflate: having no provenance to place is not
// the same as having nowhere to place it, so the first raises no advisory.
func TestStampResourceProvenance_EmptySetStampsNothingAndReportsNothing(t *testing.T) {
	in := []byte(`{"Resources": {"Plain": {"Type": "AWS::SQS::Queue"}}}`)
	for _, provenance := range []map[string]string{nil, {}} {
		body, warning, err := StampResourceProvenance("template.json", in, provenance)
		if err != nil {
			t.Fatalf("stamp: %v", err)
		}
		if string(body) != string(in) {
			t.Errorf("an empty provenance set must leave the bytes unchanged; got\n%s", body)
		}
		if warning != "" {
			t.Errorf("an empty provenance set is not a missing site, so nothing is reported; got %q", warning)
		}
	}
}

// TestStampResourceProvenance_IsDeterministic asserts the stamp is a function of
// its input alone: two runs over identical input produce identical bytes, and each
// resource's block is its own value rather than one map shared across resources.
func TestStampResourceProvenance_IsDeterministic(t *testing.T) {
	in := []byte(`{"Resources": {"A": {"Type": "AWS::SQS::Queue"}, "B": {"Type": "AWS::S3::Bucket"}}}`)
	provenance := ProvenanceSetFromNodeContext(provenanceFixtureNodeCtx())

	first, _, err := StampResourceProvenance("template.json", in, provenance)
	if err != nil {
		t.Fatalf("stamp: %v", err)
	}
	for i := 0; i < 4; i++ {
		again, _, err := StampResourceProvenance("template.json", in, provenance)
		if err != nil {
			t.Fatalf("stamp: %v", err)
		}
		if string(again) != string(first) {
			t.Fatalf("run %d produced different bytes\nfirst:\n%s\nagain:\n%s", i, first, again)
		}
	}
}

// TestStampSourcedProvenance_SkipsTheNonTemplateContentTheNormalizerVendors
// asserts the sourced wrapper reads exactly the files the normalizer treats as
// templates. A directory source's README declares no resource, but it is not a
// template that failed to carry provenance — it is a file that was never asked to,
// so it draws no advisory and its bytes are untouched.
func TestStampSourcedProvenance_SkipsTheNonTemplateContentTheNormalizerVendors(t *testing.T) {
	files := []SourcedFile{
		{Path: "README.md", Content: []byte("# not a template\n")},
		{Path: "template.yaml", Content: []byte(`{"Resources": {"Plain": {"Type": "AWS::SQS::Queue"}}}`)},
		{Path: "params.json", Content: []byte(`{"Parameters": {"Size": {"Type": "String"}}}`)},
	}
	provenance := map[string]string{"aggrega.ai/component-id": "store"}

	out, warnings, err := StampSourcedProvenance(files, true, provenance)
	if err != nil {
		t.Fatalf("stamp sourced: %v", err)
	}
	if len(out) != len(files) {
		t.Fatalf("the packed order and file count must be preserved; got %d files, want %d", len(out), len(files))
	}
	if out[0].Path != "README.md" || string(out[0].Content) != string(files[0].Content) {
		t.Errorf("non-template content must be vendored untouched; got %q = %q", out[0].Path, out[0].Content)
	}
	if !strings.Contains(string(out[1].Content), ProvenanceMetadataKey) {
		t.Errorf("the template's resource must carry provenance; got\n%s", out[1].Content)
	}
	// params.json is a template file by extension and declares no resource, so it
	// is the one advisory. The README is not, so it contributes none.
	if len(warnings) != 1 {
		t.Fatalf("want exactly one advisory (the resource-less template); got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "params.json") {
		t.Errorf("the advisory must name the resource-less template; got %q", warnings[0])
	}
}
