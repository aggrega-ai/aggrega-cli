// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cbororacle_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// A socket payload is encoded by one side of the plugin boundary and decoded by
// the other, and the two sides do not hold the same Go type: an order-preserving
// container on the compiler side is the compiler's own, and on the plugin side the
// plugin binding's. The tests below pin the consequence that matters — the bytes
// one side emits are exactly the bytes the other side emits for the same logical
// value, so either can decode the other and neither can drift into a private
// dialect.
//
// The fixtures deliberately insert keys in an order that is NOT their canonical
// sorted order, so a wrong encoding is observable: an encoder that sorted an
// order-preserving container, or a decoder that rebuilt one in sorted order, would
// produce different bytes here rather than accidentally agreeing.

func om[V any](t *testing.T, kv ...any) types.OrderedMap[V] {
	t.Helper()
	if len(kv)%2 != 0 {
		t.Fatalf("om: odd key/value count %d", len(kv))
	}
	var m types.OrderedMap[V]
	for i := 0; i < len(kv); i += 2 {
		k, ok := kv[i].(string)
		if !ok {
			t.Fatalf("om: key %d is not a string", i)
		}
		v, ok := kv[i+1].(V)
		if !ok {
			t.Fatalf("om: value for %q has the wrong type", k)
		}
		m.Set(k, v)
	}
	return m
}

// crossSide asserts that hostBytes — the canonical CBOR the compiler side
// emitted — decodes through the plugin side's own codec and re-encodes to the
// identical bytes. Both directions of one wire are covered: the plugin can read
// what the compiler wrote, and the compiler can read what the plugin writes.
func crossSide[G interface {
	DecodeCBOR([]byte) error
	EncodeCBOR() ([]byte, error)
}](t *testing.T, name string, hostBytes []byte, guest G) {
	t.Helper()
	if err := guest.DecodeCBOR(hostBytes); err != nil {
		t.Fatalf("%s: plugin-side decode of compiler-side bytes: %v", name, err)
	}
	back, err := guest.EncodeCBOR()
	if err != nil {
		t.Fatalf("%s: plugin-side re-encode: %v", name, err)
	}
	if !bytes.Equal(hostBytes, back) {
		t.Errorf("%s: the two sides of the wire disagree.\ncompiler side: %s\nplugin side:   %s",
			name, hex.EncodeToString(hostBytes), hex.EncodeToString(back))
	}
}

func TestHostGuestWireParity_Mutator(t *testing.T) {
	t.Parallel()

	in := topology.MutatorInput{
		CompilationContext: topology.CompilationContext{ReleaseName: "prod-release", Environment: "prod"},
		Graph: topology.MutatorGraph{
			// Insertion order is z, a, m — none of the three canonical positions.
			Nodes: om[topology.MutatorNode](t,
				"zeta/web", topology.MutatorNode{
					Component:       "core/web-service",
					ResolvedVersion: "1.2.3",
					Labels:          om[string](t, "tier", "web", "a", "1"),
				},
				"alpha/db", topology.MutatorNode{
					Component:       "core/database",
					ResolvedVersion: "0.9.0",
					Labels:          om[string](t, "tier", "data"),
				},
				"mid", topology.MutatorNode{Component: "core/cache", ResolvedVersion: "2.0.0"},
			),
		},
		Sources:  []topology.MutatorSource{{Path: "rules.rego", Content: "package p"}},
		Settings: json.RawMessage(`{"strict":true,"weight":0.5}`),
	}

	got, err := in.EncodeCBOR()
	if err != nil {
		t.Fatalf("compiler-side encode: %v", err)
	}
	crossSide(t, "MutatorInput", got, &abi.MutatorInput{})

	// Round-trip on the compiler side must be byte-stable too: the order-preserving
	// container is rebuilt from the wire in the order the array form carries and
	// re-encodes identically.
	var rt topology.MutatorInput
	if err := rt.DecodeCBOR(got); err != nil {
		t.Fatalf("compiler-side decode: %v", err)
	}
	again, err := rt.EncodeCBOR()
	if err != nil {
		t.Fatalf("compiler-side re-encode: %v", err)
	}
	if !bytes.Equal(got, again) {
		t.Errorf("compiler-side round-trip is not byte-stable:\nfirst:  %s\nsecond: %s",
			hex.EncodeToString(got), hex.EncodeToString(again))
	}
	// The rebuilt map carries the inserted order, which the fixture chose to differ
	// from the canonical sorted one — so this also proves the wire carries the order
	// the container held rather than a sorted one, at both levels.
	if keys := rt.Graph.Nodes.Keys(); !sameKeys(keys, []string{"zeta/web", "alpha/db", "mid"}) {
		t.Errorf("decoded node key order = %v, want the inserted order [zeta/web alpha/db mid]", keys)
	}
	if web, _ := rt.Graph.Nodes.Get("zeta/web"); !sameKeys(web.Labels.Keys(), []string{"tier", "a"}) {
		t.Errorf("decoded label key order = %v, want the inserted order [tier a]", web.Labels.Keys())
	}
	var guest abi.MutatorInput
	if err := guest.DecodeCBOR(got); err != nil {
		t.Fatalf("plugin-side decode: %v", err)
	}
	if keys := guest.Graph.Nodes.Keys(); !sameKeys(keys, []string{"zeta/web", "alpha/db", "mid"}) {
		t.Errorf("plugin-side decoded node key order = %v, want the inserted order [zeta/web alpha/db mid]", keys)
	}

	out := topology.MutatorOutput{Patches: []json.RawMessage{json.RawMessage(`[{"op":"add"}]`)}}
	ob, err := out.EncodeCBOR()
	if err != nil {
		t.Fatalf("compiler-side output encode: %v", err)
	}
	crossSide(t, "MutatorOutput", ob, &abi.MutatorOutput{})
}

func TestHostGuestWireParity_AnnotationTransform(t *testing.T) {
	t.Parallel()

	in := pipeline.AnnotationTransformInput{
		Targets: []pipeline.AnnotationTarget{{
			BoundaryPath:  "app",
			ComponentName: "web",
			Annotations:   om[string](t, "zzz", "3", "a", "1", "mm", "2"),
		}},
		BindingKeys: []string{"aggrega.ai/service-account"},
	}
	got, err := in.EncodeCBOR()
	if err != nil {
		t.Fatalf("compiler-side encode: %v", err)
	}
	crossSide(t, "AnnotationTransformInput", got, &abi.AnnotationTransformInput{})
}

// TestHostGuestWireParity_Linter pins the linter payload's byte identity across
// the boundary, in the direction each half runs in a build.
//
// The publishing-discipline configuration is the typed Policies object on both
// sides, so both write it on every input, as an empty map when no flag is set,
// and the compiler's encoding of an input decodes on the plugin side and
// re-encodes to the identical bytes. The input cases cover one with no flag set
// and one carrying flags beside a whole Release, whose components are inserted in
// an order that is not their canonical one, so a side that sorted them would
// disagree here.
//
// The findings run the other way in a build: the plugin writes the output and the
// compiler reads it through the strict decode its dispatch sites use, so that
// case encodes on the plugin side and re-encodes on the compiler side.
func TestHostGuestWireParity_Linter(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   validator.LinterInput
		// order is the component order the compiler side was handed, which the
		// plugin side must decode in that order rather than a sorted one.
		order []string
	}{
		{
			name: "no flag set",
			in: validator.LinterInput{
				RuleID:     "schema-evolution",
				EntityKind: "Component",
				Proposed:   json.RawMessage(`{"apiVersion":"aggrega.ai/v1alpha1"}`),
				SourcePath: "core/compute/web-service/1.0.0/component.yaml",
			},
		},
		{
			name: "flags set beside a whole Release",
			in: validator.LinterInput{
				RuleID:     "connection-credential-matches-target-key",
				EntityKind: "Aggregate",
				Proposed:   json.RawMessage(`{"kind":"Aggregate"}`),
				Release:    json.RawMessage(`{"kind":"Release"}`),
				Components: om[json.RawMessage](t,
					"zeta", json.RawMessage(`{"kind":"Component"}`),
					"alpha", json.RawMessage(`{"kind":"Component","n":1}`),
					"mid", json.RawMessage(`{"kind":"Component","n":2}`),
				),
				Policies: validator.Policies{
					AllowDynamicRefs:                    true,
					AllowConnectionCredentialMismatches: true,
				},
			},
			order: []string{"zeta", "alpha", "mid"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			hostBytes, err := tc.in.EncodeCBOR()
			if err != nil {
				t.Fatalf("compiler-side encode: %v", err)
			}
			crossSide(t, "LinterInput", hostBytes, &abi.LinterInput{})

			var guest abi.LinterInput
			if err := guest.DecodeCBOR(hostBytes); err != nil {
				t.Fatalf("plugin-side decode of compiler-side bytes: %v", err)
			}
			// The conversion compiles only while the two sides declare the same
			// fields, which is the shared shape this test is about.
			if got := validator.Policies(guest.Policies); got != tc.in.Policies {
				t.Errorf("plugin-side decode of the configuration = %+v, want %+v", got, tc.in.Policies)
			}
			if !bytes.Equal(guest.Proposed, tc.in.Proposed) {
				t.Errorf("plugin-side decode of the opaque payload = %q, want %q", guest.Proposed, tc.in.Proposed)
			}
			if tc.order != nil && !sameKeys(guest.Components.Keys(), tc.order) {
				t.Errorf("plugin-side decoded component order = %v, want the inserted order %v",
					guest.Components.Keys(), tc.order)
			}
		})
	}

	t.Run("findings written by the plugin side", func(t *testing.T) {
		t.Parallel()
		out := abi.LinterOutput{Findings: []abi.Diagnostic{
			{RuleID: "name-shape", Severity: "halt", Message: "the name is not kebab-case"},
			{RuleID: "category-matches-directory", Severity: "warn", Message: "the category label names another directory"},
		}}
		guestBytes, err := out.EncodeCBOR()
		if err != nil {
			t.Fatalf("plugin-side encode: %v", err)
		}
		var host validator.LinterOutput
		if err := host.DecodeCBORStrict(guestBytes); err != nil {
			t.Fatalf("compiler-side strict decode of plugin-side bytes: %v", err)
		}
		if len(host.Findings) != len(out.Findings) {
			t.Fatalf("compiler-side decode carried %d finding(s), want %d", len(host.Findings), len(out.Findings))
		}
		back, err := host.EncodeCBOR()
		if err != nil {
			t.Fatalf("compiler-side re-encode: %v", err)
		}
		if !bytes.Equal(guestBytes, back) {
			t.Errorf("LinterOutput: the two sides of the wire disagree.\nplugin side:   %s\ncompiler side: %s",
				hex.EncodeToString(guestBytes), hex.EncodeToString(back))
		}
	})
}

func TestHostGuestWireParity_Descriptor(t *testing.T) {
	t.Parallel()

	d := wasm.PluginDescriptor{
		AbiVersion:  "v1alpha1",
		PluginType:  "governance",
		Description: "cross-side descriptor fixture",
		Exports:     []wasm.PluginExport{{Name: "verdict_ir"}},
	}
	got, err := d.EncodeCBOR()
	if err != nil {
		t.Fatalf("compiler-side encode: %v", err)
	}
	crossSide(t, "PluginDescriptor", got, &abi.PluginDescriptor{})
}

// TestHostGuestWireParity_LinkEdge covers the deepest ordered shape on any
// socket: an order-preserving map whose values are themselves order-preserving
// maps. Its plugin-side counterpart nests the binding's container the same way,
// and each level is an array of entries on the wire, so this is the shape most
// likely to expose a nesting mistake in either encoder.
func TestHostGuestWireParity_LinkEdge(t *testing.T) {
	t.Parallel()

	in := rendercore.LinkPublishInput{
		Edge: rendercore.LinkEdge{
			Source:   rendercore.LinkEndpoint{Node: "web", Boundary: "app", Provider: "helm"},
			Target:   rendercore.LinkEndpoint{Node: "db", Boundary: "app", Provider: "helm"},
			Socket:   "db",
			Provides: om[string](t, "port", "5432", "host", "db.svc"),
			Inject: om[types.OrderedMap[string]](t,
				"env", om[string](t, "PGPORT", "5432", "DB", "app"),
				"a", om[string](t, "X", "1"),
			),
		},
	}
	got, err := in.EncodeCBOR()
	if err != nil {
		t.Fatalf("compiler-side encode: %v", err)
	}
	crossSide(t, "LinkPublishInput", got, &abi.LinkPublishInput{})
}
