// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package entitysynth_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/entitysynth"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmfixture"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The tests in this file drive the claim a provider makes that it honours omit
// entries through every consumer of it, with real guests rather than hand-built
// descriptors: the load gate's comparison against a Plugin entity's claim, in both
// directions, and the entity generated from a guest's describe(). The claim is
// compared exactly, as its sibling provider-wide claims are, so an entity can
// neither promise support its plugin lacks nor hide support its plugin declares.

// guestBytes builds one of the local provider guests under the shared fixture
// directory and returns its bytes.
func guestBytes(t *testing.T, dir, artifact string) []byte {
	t.Helper()
	src, err := filepath.Abs(filepath.Join("..", "testdata", dir))
	if err != nil {
		t.Fatalf("resolve guest source: %v", err)
	}
	b, err := os.ReadFile(wasmfixture.EnsureLocalGuestBuilt(t, src, artifact))
	if err != nil {
		t.Fatalf("read built guest %s: %v", artifact, err)
	}
	return b
}

// omitsGuest is a provider whose describe() claims to honour omit entries.
func omitsGuest(t *testing.T) []byte {
	return guestBytes(t, "omits-provider", "omits-provider.wasm")
}

// decliningGuest is a provider whose describe() makes no such claim. It is the
// guest built to drive the host's refusals of non-conforming output; its
// self-report is conforming, which is what lets it load here.
func decliningGuest(t *testing.T) []byte {
	return guestBytes(t, "nonconforming-provider", "nonconforming-provider.wasm")
}

// load runs the entity-fronted load gate over a guest with the given authored
// contract claim and returns the validated descriptor.
func load(t *testing.T, identity string, bytes []byte, claim *wasm.ProviderContract) (wasm.PluginDescriptor, error) {
	t.Helper()
	ctx := context.Background()
	spec := wasm.PluginSpec{
		Name:       identity,
		PluginType: wasm.PluginTypeProvider,
		Bytes:      bytes,
		Contract:   &wasm.EntityContractClaim{Contract: claim},
	}
	plugin, desc, err := wasm.NewCachingLoader(nil).LoadAndDescribe(ctx, spec)
	if err != nil {
		return wasm.PluginDescriptor{}, err
	}
	_ = plugin.Close(ctx)
	return desc, nil
}

// omitsGuestClaim is the omits guest's own contract as an entity authors it, with
// the omits claim set as given.
func omitsGuestClaim(omits bool) *wasm.ProviderContract {
	return &wasm.ProviderContract{
		InputKinds:       wasm.InputKinds{Sourced: true},
		BoundaryAssembly: wasm.BoundaryAssemblyNone,
		ReusableInstance: true,
		Omits:            omits,
	}
}

// decliningGuestClaim is the declining guest's own contract as an entity authors
// it, with the omits claim set as given.
func decliningGuestClaim(omits bool) *wasm.ProviderContract {
	return &wasm.ProviderContract{
		InputKinds:           wasm.InputKinds{Sourced: true, Synthesized: true},
		BoundaryAssembly:     wasm.BoundaryAssemblyNone,
		ReusableInstance:     true,
		CrossBoundaryLinking: true,
		Omits:                omits,
	}
}

// TestAGuestClaimingOmitsLoadsAndTheEntityGeneratedFromItCarriesTheClaim loads
// the omits guest against an entity making the same claim, then generates the
// Plugin entity from the guest's describe() and reads the claim back at
// spec.provider.contract.omits, on the value and on its serialized form.
func TestAGuestClaimingOmitsLoadsAndTheEntityGeneratedFromItCarriesTheClaim(t *testing.T) {
	const identity = "provider/omits-example:1.0.0"
	desc, err := load(t, identity, omitsGuest(t), omitsGuestClaim(true))
	if err != nil {
		t.Fatalf("a guest whose entity makes the claim it declares must load: %v", err)
	}
	if desc.Contract == nil || !desc.Contract.Omits {
		t.Fatalf("the loaded descriptor does not carry the omits claim: %+v", desc.Contract)
	}

	overlay := types.Plugin{Metadata: types.PluginMetadata{Name: "omits-example"}}
	entity, err := entitysynth.MergeForPublish(desc, "", nil, overlay, identity)
	if err != nil {
		t.Fatalf("generate the entity: %v", err)
	}
	if !entity.Spec.Provider.Contract.Omits {
		t.Fatalf("the generated entity's contract lost the omits claim: %+v", entity.Spec.Provider.Contract)
	}
	raw, err := json.Marshal(entity.Spec.Provider.Contract)
	if err != nil {
		t.Fatalf("marshal the generated contract: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("read the generated contract back: %v", err)
	}
	if fields["omits"] != true {
		t.Errorf("the serialized entity contract does not carry omits: true: %s", raw)
	}
}

// TestTheLoadRefusesAnEntityWhoseOmitsClaimDisagreesWithItsGuest drives both
// disagreeing cells through the load gate, each with the guest that makes the
// opposite claim: an entity hiding the support its guest declares, and an entity
// promising support its guest does not declare. Each is refused under the
// contract-mismatch condition, naming the field and both values.
func TestTheLoadRefusesAnEntityWhoseOmitsClaimDisagreesWithItsGuest(t *testing.T) {
	cases := []struct {
		name     string
		identity string
		bytes    func(*testing.T) []byte
		claim    *wasm.ProviderContract
		want     string
	}{
		{
			name:     "an entity not claiming support its guest declares",
			identity: "provider/omits-example:1.0.0",
			bytes:    omitsGuest,
			claim:    omitsGuestClaim(false),
			want:     "declares omits false but the plugin's describe() reports true",
		},
		{
			name:     "an entity claiming support its guest does not declare",
			identity: "provider/nonconforming:1.0.0",
			bytes:    decliningGuest,
			claim:    decliningGuestClaim(true),
			want:     "declares omits true but the plugin's describe() reports false",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := load(t, c.identity, c.bytes(t), c.claim)
			if !errors.Is(err, wasm.ErrWasmContractMismatch()) {
				t.Fatalf("load = %v; want the contract-mismatch refusal", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("the refusal does not name the field and both values (%q): %v", c.want, err)
			}
			if !strings.Contains(err.Error(), c.identity) {
				t.Errorf("the refusal does not name the entity: %v", err)
			}
		})
	}

	// Positive control for the second guest: with the claim it does make, it
	// loads, so the refusal above is the omits field's and nothing else's.
	if _, err := load(t, "provider/nonconforming:1.0.0", decliningGuest(t), decliningGuestClaim(false)); err != nil {
		t.Fatalf("the declining guest must load against an entity making its own claim: %v", err)
	}
}

// TestAContractClaimingOnlyOmitsSupportIsNotAnAbsentContract holds the two
// answers to "does this provider author a contract" to the new claim: the
// describe() side's, which lists the fields it reads, and the entity side's,
// which compares against the zero value and so counts every generated field.
// A contract carrying nothing but the claim is a contract on both, so the
// generated entity keeps it rather than dropping it as empty.
func TestAContractClaimingOnlyOmitsSupportIsNotAnAbsentContract(t *testing.T) {
	if !entitysynth.IsZeroContract(wasm.ProviderContract{}) {
		t.Fatal("an empty describe() contract must read as absent")
	}
	if entitysynth.IsZeroContract(wasm.ProviderContract{Omits: true}) {
		t.Error("a describe() contract claiming only omits support reads as absent")
	}
	if (types.ProviderContract{Omits: true}).IsZero() {
		t.Error("an entity contract claiming only omits support reads as absent")
	}

	desc := wasm.PluginDescriptor{
		AbiVersion: wasm.ABIVersion,
		PluginType: string(wasm.PluginTypeProvider),
		Exports:    []wasm.PluginExport{{Name: "render_node"}},
		Contract:   &wasm.ProviderContract{Omits: true},
		Metadata:   &wasm.PluginDescriptorMeta{Version: "1.0.0"},
	}
	overlay := types.Plugin{Metadata: types.PluginMetadata{Name: "omits-only"}}
	entity, err := entitysynth.MergeForPublish(desc, "", nil, overlay, "provider/omits-only:1.0.0")
	if err != nil {
		t.Fatalf("generate the entity: %v", err)
	}
	if !entity.Spec.Provider.Contract.Omits {
		t.Errorf("the generated entity dropped a contract whose only claim is omits support: %+v", entity.Spec.Provider.Contract)
	}
}
