// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mutatorcore_test

import (
	"encoding/json"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/plugins/mutatorcore"
)

// threeNodeGraph returns a fixed label-projection graph the tests evaluate
// against: two nodes carrying a tier label plus an unlabelled cache node whose
// component is absent from the tagger mapping.
func threeNodeGraph() map[string]abi.MutatorNode {
	return map[string]abi.MutatorNode{
		"web":   {Component: "core/web", Labels: map[string]string{"tier": "frontend"}},
		"db":    {Component: "core/db", Labels: map[string]string{"tier": "backend"}},
		"cache": {Component: "core/cache", Labels: map[string]string{}},
	}
}

func rawList(t *testing.T, patches []json.RawMessage) []string {
	t.Helper()
	out := make([]string, len(patches))
	for i, p := range patches {
		out[i] = string(p)
	}
	return out
}

func assertEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("patch count = %d, want %d\n got: %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("patch[%d] = %s\n         want %s", i, got[i], want[i])
		}
	}
}

// TestLabelInjector_FromSettings proves the injector reads its rule set from the
// settings object, matches on a label subset, escapes a "/" in a set key per RFC
// 6901, and emits set keys in sorted order for the matching node only.
func TestLabelInjector_FromSettings(t *testing.T) {
	settings := json.RawMessage(`{"rules":[{"match":{"tier":"frontend"},"set":{"compliance.aggrega/pci":"true","team":"web"}}]}`)
	patches, err := mutatorcore.LabelInjectorPatches(threeNodeGraph(), settings, nil)
	if err != nil {
		t.Fatalf("LabelInjectorPatches: %v", err)
	}
	assertEqual(t, rawList(t, patches), []string{
		`[{"op":"add","path":"/nodes/web/labels/compliance.aggrega~1pci","value":"true"}]`,
		`[{"op":"add","path":"/nodes/web/labels/team","value":"web"}]`,
	})
}

// TestLabelInjector_FromSources proves the injector reads the same rule set from
// a definition file when settings carry none, parsing the file through the
// shared YAML-to-tree conversion.
func TestLabelInjector_FromSources(t *testing.T) {
	src := abi.MutatorSource{Path: "rules.yaml", Content: "rules:\n  - match:\n      tier: backend\n    set:\n      data-class: sensitive\n"}
	patches, err := mutatorcore.LabelInjectorPatches(threeNodeGraph(), nil, []abi.MutatorSource{src})
	if err != nil {
		t.Fatalf("LabelInjectorPatches: %v", err)
	}
	assertEqual(t, rawList(t, patches), []string{
		`[{"op":"add","path":"/nodes/db/labels/data-class","value":"sensitive"}]`,
	})
}

// TestLabelInjector_EmptyMatchTagsEveryNode proves an empty match is a subset of
// every label set, so an org-wide rule tags all nodes in sorted node-ID order.
func TestLabelInjector_EmptyMatchTagsEveryNode(t *testing.T) {
	settings := json.RawMessage(`{"rules":[{"match":{},"set":{"org":"acme"}}]}`)
	patches, err := mutatorcore.LabelInjectorPatches(threeNodeGraph(), settings, nil)
	if err != nil {
		t.Fatalf("LabelInjectorPatches: %v", err)
	}
	assertEqual(t, rawList(t, patches), []string{
		`[{"op":"add","path":"/nodes/cache/labels/org","value":"acme"}]`,
		`[{"op":"add","path":"/nodes/db/labels/org","value":"acme"}]`,
		`[{"op":"add","path":"/nodes/web/labels/org","value":"acme"}]`,
	})
}

// TestSelectorTagger_MappingWithDefault proves the tagger sets the tag label
// from the component-to-tag map and falls back to the settings default for a
// node whose component is unmapped, in sorted node-ID order.
func TestSelectorTagger_MappingWithDefault(t *testing.T) {
	src := abi.MutatorSource{Path: "mapping.yaml", Content: "core/web: frontend-tier\ncore/db: data-tier\n"}
	settings := json.RawMessage(`{"default":"unknown"}`)
	patches, err := mutatorcore.SelectorTaggerPatches(threeNodeGraph(), settings, []abi.MutatorSource{src})
	if err != nil {
		t.Fatalf("SelectorTaggerPatches: %v", err)
	}
	assertEqual(t, rawList(t, patches), []string{
		`[{"op":"add","path":"/nodes/cache/labels/tag","value":"unknown"}]`,
		`[{"op":"add","path":"/nodes/db/labels/tag","value":"data-tier"}]`,
		`[{"op":"add","path":"/nodes/web/labels/tag","value":"frontend-tier"}]`,
	})
}

// TestSelectorTagger_NoDefaultSkipsUnmapped proves a node whose component is
// unmapped is left untouched when no default is configured.
func TestSelectorTagger_NoDefaultSkipsUnmapped(t *testing.T) {
	src := abi.MutatorSource{Path: "mapping.yaml", Content: "core/web: frontend-tier\ncore/db: data-tier\n"}
	patches, err := mutatorcore.SelectorTaggerPatches(threeNodeGraph(), nil, []abi.MutatorSource{src})
	if err != nil {
		t.Fatalf("SelectorTaggerPatches: %v", err)
	}
	assertEqual(t, rawList(t, patches), []string{
		`[{"op":"add","path":"/nodes/db/labels/tag","value":"data-tier"}]`,
		`[{"op":"add","path":"/nodes/web/labels/tag","value":"frontend-tier"}]`,
	})
}

// TestDeterministicAcrossRuns proves both guests emit a byte-identical patch
// list across repeated runs of identical input regardless of the input map's
// iteration order.
func TestDeterministicAcrossRuns(t *testing.T) {
	settings := json.RawMessage(`{"rules":[{"match":{},"set":{"b":"2","a":"1","c":"3"}}]}`)
	first, err := mutatorcore.LabelInjectorPatches(threeNodeGraph(), settings, nil)
	if err != nil {
		t.Fatalf("LabelInjectorPatches: %v", err)
	}
	for i := 0; i < 8; i++ {
		again, err := mutatorcore.LabelInjectorPatches(threeNodeGraph(), settings, nil)
		if err != nil {
			t.Fatalf("LabelInjectorPatches: %v", err)
		}
		assertEqual(t, rawList(t, again), rawList(t, first))
	}
}
