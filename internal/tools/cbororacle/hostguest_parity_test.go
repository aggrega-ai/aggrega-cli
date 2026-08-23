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
// container on the compiler side is a plain string-keyed map on the plugin side.
// The tests below pin the consequence that matters — the bytes one side emits are
// exactly the bytes the other side emits for the same logical value, so either
// can decode the other and neither can drift into a private dialect.
//
// The fixtures deliberately insert keys in an order that is NOT their canonical
// order, so a wrong encoding is observable: an encoder that carried insertion
// order instead of canonical order would produce different bytes here rather
// than accidentally agreeing.

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
	// container is rebuilt from the wire in canonical order and re-encodes identically.
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
	// The rebuilt map carries canonical order, which the fixture's insertion order
	// was chosen to differ from — so this also proves the wire order is canonical
	// rather than authored.
	if keys := rt.Graph.Nodes.Keys(); len(keys) != 3 || keys[0] != "mid" {
		t.Errorf("decoded node key order = %v, want canonical order with the shortest key first", keys)
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

// TestHostGuestWireParity_Linter pins a known asymmetry rather than asserting
// the byte-identity the other pairs hold, and the asymmetry is not a property of
// the transport: it is present on the JSON wire too.
//
// The linter payload's publishing-discipline configuration is optional, and the
// two sides realize it differently — the compiler side carries it as an opaque
// payload, which has an absent state and is dropped when absent; the plugin side
// realizes it as a typed object value, which has no absent state and therefore
// always emits the key with an empty object. So a payload the compiler emits
// without the key re-emits from the plugin side WITH it.
//
// The direction that runs in a build is unaffected: the compiler encodes this
// payload and the plugin decodes it, and that decode succeeds. The reverse
// direction — a plugin emitting this payload for the compiler to read — is not a
// path any socket uses. The assertions below fix the current shape in place so
// that closing the asymmetry, or widening it, is a deliberate change with a
// failing test to acknowledge rather than a silent drift.
func TestHostGuestWireParity_Linter(t *testing.T) {
	t.Parallel()

	in := validator.LinterInput{
		RuleID:     "schema-evolution",
		EntityKind: "Component",
		Proposed:   json.RawMessage(`{"apiVersion":"aggrega.ai/v1alpha1"}`),
		SourcePath: "core/compute/web-service/1.0.0/component.yaml",
	}
	hostBytes, err := in.EncodeCBOR()
	if err != nil {
		t.Fatalf("compiler-side encode: %v", err)
	}

	var guest abi.LinterInput
	if err := guest.DecodeCBOR(hostBytes); err != nil {
		t.Fatalf("plugin-side decode of compiler-side bytes: %v", err)
	}
	if guest.RuleID != in.RuleID || guest.EntityKind != in.EntityKind || guest.SourcePath != in.SourcePath {
		t.Errorf("plugin-side decode lost a field: %+v", guest)
	}
	if !bytes.Equal(guest.Proposed, in.Proposed) {
		t.Errorf("plugin-side decode of the opaque payload = %q, want %q", guest.Proposed, in.Proposed)
	}

	guestBytes, err := guest.EncodeCBOR()
	if err != nil {
		t.Fatalf("plugin-side re-encode: %v", err)
	}
	// "policies" as a CBOR text string: major type 3, length 8.
	policiesKey := append([]byte{0x60 | 8}, "policies"...)
	if bytes.Contains(hostBytes, policiesKey) {
		t.Errorf("compiler side now emits the optional configuration key; the asymmetry this test pins has changed: %s",
			hex.EncodeToString(hostBytes))
	}
	if !bytes.Contains(guestBytes, policiesKey) {
		t.Errorf("plugin side no longer emits the optional configuration key; the asymmetry this test pins has changed: %s",
			hex.EncodeToString(guestBytes))
	}
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
// maps. Its plugin-side counterpart is a plain map of plain maps, so this is the
// shape most likely to expose a nesting mistake in either encoder.
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
