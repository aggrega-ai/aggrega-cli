// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver_test

import (
	"encoding/json"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/entitysynth"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// declaresSegment builds the carrier an attachment-directory declaration holds.
// A nil carrier declares nothing, and declares("") is a claim about layout rather
// than the absence of one, so the two are spelled differently at every call.
func declaresSegment(segment string) *string { return &segment }

// segmentText renders a carrier for a failure message without collapsing the two
// empty-ish states into one another, which is exactly what a %q of a
// dereferenced pointer would do.
func segmentText(carrier *string) string {
	if carrier == nil {
		return "<undeclared>"
	}
	return `declared "` + *carrier + `"`
}

// sameSegment compares two carriers by declaredness and then by the directory
// named. It deliberately does NOT compare the pointers: both operands here are
// the same Go type, so a pointer comparison type-checks, reads like a match, and
// silently tests identity — two separately decoded carriers naming one directory
// would then read as a disagreement. Every assertion below routes through this.
func sameSegment(a, b *string) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a == nil || *a == *b
}

// descriptorWithSegment builds a minimal but publish-valid provider descriptor
// whose render-behavior contract declares segment. The contract carries a real
// input kind and assembly mode as well, so it is not the zero contract — a zero
// contract is dropped from the entity entirely and the cross-check is then
// skipped, which would make every assertion below pass vacuously.
func descriptorWithSegment(segment *string) wasm.PluginDescriptor {
	return wasm.PluginDescriptor{
		AbiVersion: wasm.ABIVersionV1Alpha1,
		PluginType: string(wasm.PluginTypeProvider),
		Exports:    []wasm.PluginExport{{Name: "render_node"}},
		Metadata:   &wasm.PluginDescriptorMeta{Version: "0.5.0"},
		Contract: &wasm.ProviderContract{
			InputKinds:        wasm.InputKinds{Sourced: true},
			BoundaryAssembly:  wasm.BoundaryAssemblyNone,
			ReusableInstance:  true,
			AttachmentSegment: segment,
		},
	}
}

func publishOverlay() types.Plugin {
	return types.Plugin{Metadata: types.PluginMetadata{Name: "helm"}}
}

// TestPluginEntityAttachmentSegmentRoundTripsAllThreeStates is the end-to-end
// proof that a provider guest's attachment-directory declaration survives the
// full publish-and-load loop in every one of its three states, and that the
// load-time contract cross-check then AGREES with the guest it was generated
// from.
//
// The loop is the real one, driven through exported production entry points
// rather than a hand-assembled intermediate: the guest's describe() contract is
// merged into a publish-ready entity, the entity is serialized and read back in
// each encoding it travels in, the decoded entity is projected onto the neutral
// authored claim and then onto the cross-check's own shape, and the result is
// compared against the descriptor that started it.
//
// 🔴 The state that matters is the middle one. A guest declaring that its target
// wants no enclosing directory declares the EMPTY string, and an empty string is
// exactly what an `omitempty` tag drops. Held as a plain string, the key is
// written as an absent key, read back as no declaration at all, and the
// cross-check then halts on a disagreement between a guest and the entity
// generated from it — with regenerating unable to fix it, because the entity had
// no way to express the state. Held by reference, a nil carrier omits the key and
// a carrier holding the empty string still writes it, so the state survives.
//
// Both encodings are asserted because the entity travels in both and their
// `omitempty` implementations are separate code in separate libraries: agreement
// between them is a measurement, not a property either one guarantees.
func TestPluginEntityAttachmentSegmentRoundTripsAllThreeStates(t *testing.T) {
	t.Parallel()

	const identity = "provider/helm:0.5.0"

	cases := []struct {
		name string
		// segment is the guest's declaration.
		segment *string
		// wantKey is whether the serialized entity carries the authored key at
		// all. It is asserted on the bytes rather than inferred from the decoded
		// value, because an omitted key and a key holding the empty string decode
		// differently and only the bytes distinguish which one was written.
		wantKey bool
	}{
		{
			name:    "declares nothing: the key is omitted and the compiler keeps its own default",
			segment: nil,
			wantKey: false,
		},
		{
			name:    "declares no enclosing directory: the key is written EMPTY and survives",
			segment: declaresSegment(""),
			wantKey: true,
		},
		{
			name:    "names a directory: the key is written with that directory",
			segment: declaresSegment("addons"),
			wantKey: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			desc := descriptorWithSegment(tc.segment)

			entity, err := entitysynth.MergeForPublish(desc, "", nil, publishOverlay(), identity)
			if err != nil {
				t.Fatalf("MergeForPublish: %v", err)
			}
			if got := entity.Spec.Provider.Contract.AttachmentSegment; !sameSegment(got, tc.segment) {
				t.Fatalf("synthesized entity: attachmentSegment %s, want %s",
					segmentText(got), segmentText(tc.segment))
			}

			for _, enc := range []struct {
				name      string
				marshal   func(any) ([]byte, error)
				unmarshal func([]byte, any) error
			}{
				{"yaml", yaml.Marshal, func(b []byte, v any) error { return yaml.Unmarshal(b, v) }},
				{"json", json.Marshal, func(b []byte, v any) error { return json.Unmarshal(b, v) }},
			} {
				t.Run(enc.name, func(t *testing.T) {
					t.Parallel()

					raw, err := enc.marshal(entity)
					if err != nil {
						t.Fatalf("marshal: %v", err)
					}
					if gotKey := strings.Contains(string(raw), "attachmentSegment"); gotKey != tc.wantKey {
						t.Errorf("serialized entity carries the attachmentSegment key = %t, want %t\nbytes:\n%s",
							gotKey, tc.wantKey, raw)
					}

					var decoded types.Plugin
					if err := enc.unmarshal(raw, &decoded); err != nil {
						t.Fatalf("unmarshal: %v", err)
					}
					if got := decoded.Spec.Provider.Contract.AttachmentSegment; !sameSegment(got, tc.segment) {
						t.Fatalf("decoded entity: attachmentSegment %s, want %s",
							segmentText(got), segmentText(tc.segment))
					}

					// The load path, through the two exported projections it
					// actually uses: entity → neutral authored claim → the
					// cross-check's own shape.
					claim := resolver.PluginContractClaim(decoded)
					if claim == nil {
						t.Fatal("PluginContractClaim returned nil, so the cross-check would be skipped and every assertion here would pass vacuously")
					}
					entityClaim := resolver.WasmContractClaim(claim)
					if entityClaim == nil || entityClaim.Contract == nil {
						t.Fatal("WasmContractClaim carried no contract, so the plugin-level cross-check would be skipped")
					}
					if got := entityClaim.Contract.AttachmentSegment; !sameSegment(got, tc.segment) {
						t.Fatalf("claim reaching the cross-check: attachmentSegment %s, want %s",
							segmentText(got), segmentText(tc.segment))
					}

					// The whole point: the entity generated from this guest must
					// agree with that same guest at load.
					if err := wasm.CrossCheckContract(identity, *entityClaim, desc); err != nil {
						t.Errorf("cross-check rejected an entity generated from the very guest it is compared against: %v", err)
					}
				})
			}
		})
	}
}

// TestPluginEntityAttachmentSegmentRoundTripNegativeControl is the positive
// control for the test above: it shows that the round-trip assertion is capable
// of failing.
//
// The agreement asserted above is only evidence if a DISagreement is refused, and
// the pairing that matters is the one the three-state carrier makes expressible —
// a guest declaring no enclosing directory against an entity declaring nothing at
// all. Under the two-state shape that pair was unreachable from authoring, so a
// green on the loop above could have meant the states were being collapsed rather
// than carried. Here the entity's carrier is overwritten AFTER synthesis, so the
// two operands genuinely disagree, and the cross-check must halt.
func TestPluginEntityAttachmentSegmentRoundTripNegativeControl(t *testing.T) {
	t.Parallel()

	const identity = "provider/helm:0.5.0"

	cases := []struct {
		name string
		// guest is what describe() reports; entity is what the authored entity
		// claims after being tampered with.
		guest, entity *string
		wantMessage   string
	}{
		{
			name:        "guest declares no enclosing directory, entity declares nothing",
			guest:       declaresSegment(""),
			entity:      nil,
			wantMessage: "whether attachment_segment is declared",
		},
		{
			name:        "guest declares nothing, entity declares no enclosing directory",
			guest:       nil,
			entity:      declaresSegment(""),
			wantMessage: "whether attachment_segment is declared",
		},
		{
			name:        "guest names a directory, entity names a different one",
			guest:       declaresSegment("addons"),
			entity:      declaresSegment("charts"),
			wantMessage: "declares attachment_segment",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			desc := descriptorWithSegment(tc.guest)
			entity, err := entitysynth.MergeForPublish(desc, "", nil, publishOverlay(), identity)
			if err != nil {
				t.Fatalf("MergeForPublish: %v", err)
			}
			entity.Spec.Provider.Contract.AttachmentSegment = tc.entity

			claim := resolver.PluginContractClaim(entity)
			if claim == nil {
				t.Fatal("PluginContractClaim returned nil, so nothing would be cross-checked")
			}
			entityClaim := resolver.WasmContractClaim(claim)
			if entityClaim == nil || entityClaim.Contract == nil {
				t.Fatal("WasmContractClaim carried no contract, so nothing would be cross-checked")
			}

			err = wasm.CrossCheckContract(identity, *entityClaim, desc)
			if err == nil {
				t.Fatalf("expected the cross-check to halt on %s versus %s, got nil",
					segmentText(tc.guest), segmentText(tc.entity))
			}
			if !strings.Contains(err.Error(), tc.wantMessage) {
				t.Errorf("halt should report %q; got: %v", tc.wantMessage, err)
			}
		})
	}
}
