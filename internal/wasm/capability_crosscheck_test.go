// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"errors"
	"strings"
	"testing"
)

// TestCrossCheckCapabilities exercises the single contract check the load gate
// and the publish path share: an entity's authored capability claim against a
// plugin's self-reported describe() descriptor.
func TestCrossCheckCapabilities(t *testing.T) {
	t.Parallel()

	desc := PluginDescriptor{
		ABIVersion: ABIVersionV1Alpha1,
		PluginType: PluginTypeFRPStep,
		Exports: []PluginExport{
			{Name: "step", Operates: operatesPatch, Scope: ScopeOwn, Selector: "the plugin's own description"},
			{Name: "extra", Operates: operatesEmit, Scope: ScopeHost},
		},
	}

	cases := []struct {
		name      string
		claim     EntityCapabilityClaim
		wantErr   bool
		wantField string // substring expected in the error message
	}{
		{
			name: "agreeing claim returns nil",
			claim: EntityCapabilityClaim{Exports: []EntityExportClaim{
				{Name: "step", Operates: operatesPatch, Scope: ScopeOwn},
			}},
		},
		{
			name: "claim omitting an export the descriptor declares returns nil (entity-claim subset of describe)",
			claim: EntityCapabilityClaim{Exports: []EntityExportClaim{
				{Name: "step", Operates: operatesPatch, Scope: ScopeOwn},
			}},
		},
		{
			name: "selector-only difference does not halt",
			claim: EntityCapabilityClaim{Exports: []EntityExportClaim{
				// No selector field on the claim at all; the descriptor carries
				// one. Selector is excluded from the cross-check.
				{Name: "step", Operates: operatesPatch, Scope: ScopeOwn},
			}},
		},
		{
			name: "operates disagrees halts",
			claim: EntityCapabilityClaim{Exports: []EntityExportClaim{
				{Name: "step", Operates: operatesEmit, Scope: ScopeOwn},
			}},
			wantErr:   true,
			wantField: "operates",
		},
		{
			name: "scope disagrees halts",
			claim: EntityCapabilityClaim{Exports: []EntityExportClaim{
				{Name: "step", Operates: operatesPatch, Scope: ScopeHost},
			}},
			wantErr:   true,
			wantField: "scope",
		},
		{
			name: "claimed export absent from describe halts",
			claim: EntityCapabilityClaim{Exports: []EntityExportClaim{
				{Name: "does_not_exist", Operates: operatesPatch, Scope: ScopeOwn},
			}},
			wantErr:   true,
			wantField: "does not declare",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := CrossCheckCapabilities("frp/patch:1.0.0", tc.claim, desc)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !errors.Is(err, ErrWasmCapabilityMismatch) {
				t.Fatalf("error does not wrap ErrWasmCapabilityMismatch: %v", err)
			}
			if !strings.Contains(err.Error(), "frp/patch:1.0.0") {
				t.Errorf("error should name the entity identity; got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantField) {
				t.Errorf("error should mention %q; got: %v", tc.wantField, err)
			}
		})
	}
}

// TestCrossCheckCapabilities_PluginLevel exercises the plugin-level
// capabilities comparison: an authored capabilities claim is checked
// field-by-field against the descriptor's self-report, and a nil claim skips
// the plugin-level comparison entirely.
func TestCrossCheckCapabilities_PluginLevel(t *testing.T) {
	t.Parallel()

	desc := PluginDescriptor{
		ABIVersion: ABIVersionV1Alpha1,
		PluginType: PluginTypeProvider,
		Exports:    []PluginExport{{Name: "render_node"}},
		Capabilities: Capabilities{
			InputKinds:         InputKinds{Sourced: true, Synthesized: false, ArtifactsStandalone: false},
			BoundaryAssembly:   BoundaryAssemblyNest,
			ArtifactDelimiters: &ArtifactDelimiters{Left: "[[", Right: "]]"},
		},
	}

	cases := []struct {
		name      string
		caps      *Capabilities
		wantErr   bool
		wantField string
	}{
		{
			name: "agreeing plugin-level claim returns nil",
			caps: &Capabilities{
				InputKinds:         InputKinds{Sourced: true},
				BoundaryAssembly:   BoundaryAssemblyNest,
				ArtifactDelimiters: &ArtifactDelimiters{Left: "[[", Right: "]]"},
			},
		},
		{
			name: "nil plugin-level claim skips the comparison",
			caps: nil,
		},
		{
			name: "input_kinds.synthesized disagrees halts",
			caps: &Capabilities{
				InputKinds:         InputKinds{Sourced: true, Synthesized: true},
				BoundaryAssembly:   BoundaryAssemblyNest,
				ArtifactDelimiters: &ArtifactDelimiters{Left: "[[", Right: "]]"},
			},
			wantErr:   true,
			wantField: "input_kinds.synthesized",
		},
		{
			name: "boundary_assembly disagrees halts",
			caps: &Capabilities{
				InputKinds:         InputKinds{Sourced: true},
				BoundaryAssembly:   BoundaryAssemblyMerge,
				ArtifactDelimiters: &ArtifactDelimiters{Left: "[[", Right: "]]"},
			},
			wantErr:   true,
			wantField: "boundary_assembly",
		},
		{
			name: "artifact_delimiters value disagrees halts",
			caps: &Capabilities{
				InputKinds:         InputKinds{Sourced: true},
				BoundaryAssembly:   BoundaryAssemblyNest,
				ArtifactDelimiters: &ArtifactDelimiters{Left: "{{", Right: "}}"},
			},
			wantErr:   true,
			wantField: "artifact_delimiters",
		},
		{
			name: "artifact_delimiters presence disagrees halts",
			caps: &Capabilities{
				InputKinds:       InputKinds{Sourced: true},
				BoundaryAssembly: BoundaryAssemblyNest,
			},
			wantErr:   true,
			wantField: "artifact_delimiters",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := CrossCheckCapabilities("provider/helm-chart:1.0.0", EntityCapabilityClaim{Capabilities: tc.caps}, desc)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !errors.Is(err, ErrWasmCapabilityMismatch) {
				t.Fatalf("error does not wrap ErrWasmCapabilityMismatch: %v", err)
			}
			if !strings.Contains(err.Error(), "provider/helm-chart:1.0.0") {
				t.Errorf("error should name the entity identity; got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantField) {
				t.Errorf("error should mention %q; got: %v", tc.wantField, err)
			}
		})
	}
}

// TestCrossCheckCapabilities_CrossBoundaryLinking exercises the cross-boundary
// linking arm of the plugin-level comparison: the entity's authored
// crossBoundaryLinking claim is checked against the descriptor's self-reported
// cross_boundary_linking flag, and a disagreement in either direction halts.
func TestCrossCheckCapabilities_CrossBoundaryLinking(t *testing.T) {
	t.Parallel()

	// A descriptor that advertises cross-boundary linking exports both link
	// exports and reports the flag; the crosscheck compares the flag directly.
	linkingDesc := PluginDescriptor{
		ABIVersion: ABIVersionV1Alpha1,
		PluginType: PluginTypeProvider,
		Exports: []PluginExport{
			{Name: "render_node"},
			{Name: LinkPublishExport},
			{Name: LinkResolveExport},
		},
		Capabilities: Capabilities{
			InputKinds:           InputKinds{Sourced: true},
			BoundaryAssembly:     BoundaryAssemblyMerge,
			CrossBoundaryLinking: true,
		},
	}
	// A descriptor that wires no cross-boundary edges reports the flag false.
	nonLinkingDesc := PluginDescriptor{
		ABIVersion: ABIVersionV1Alpha1,
		PluginType: PluginTypeProvider,
		Exports:    []PluginExport{{Name: "render_node"}},
		Capabilities: Capabilities{
			InputKinds:       InputKinds{Sourced: true},
			BoundaryAssembly: BoundaryAssemblyMerge,
		},
	}

	cases := []struct {
		name      string
		desc      PluginDescriptor
		caps      *Capabilities
		wantErr   bool
		wantField string
	}{
		{
			name: "entity claims linking, descriptor advertises it: match",
			desc: linkingDesc,
			caps: &Capabilities{
				InputKinds:           InputKinds{Sourced: true},
				BoundaryAssembly:     BoundaryAssemblyMerge,
				CrossBoundaryLinking: true,
			},
		},
		{
			name: "entity claims linking, descriptor does not advertise it: halt",
			desc: nonLinkingDesc,
			caps: &Capabilities{
				InputKinds:           InputKinds{Sourced: true},
				BoundaryAssembly:     BoundaryAssemblyMerge,
				CrossBoundaryLinking: true,
			},
			wantErr:   true,
			wantField: "cross_boundary_linking",
		},
		{
			name: "descriptor advertises linking, entity does not claim it: halt",
			desc: linkingDesc,
			caps: &Capabilities{
				InputKinds:       InputKinds{Sourced: true},
				BoundaryAssembly: BoundaryAssemblyMerge,
			},
			wantErr:   true,
			wantField: "cross_boundary_linking",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := CrossCheckCapabilities("provider/cfn-renderer:1.0.0", EntityCapabilityClaim{Capabilities: tc.caps}, tc.desc)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !errors.Is(err, ErrWasmCapabilityMismatch) {
				t.Fatalf("error does not wrap ErrWasmCapabilityMismatch: %v", err)
			}
			if !strings.Contains(err.Error(), "provider/cfn-renderer:1.0.0") {
				t.Errorf("error should name the entity identity; got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantField) {
				t.Errorf("error should mention %q; got: %v", tc.wantField, err)
			}
		})
	}
}

// TestCrossCheckCapabilities_EmptyClaim proves an entity with no authored
// exports declares no claim and never halts, regardless of what the plugin
// reports.
func TestCrossCheckCapabilities_EmptyClaim(t *testing.T) {
	t.Parallel()
	desc := PluginDescriptor{
		ABIVersion: ABIVersionV1Alpha1,
		PluginType: PluginTypeFRPStep,
		Exports:    []PluginExport{{Name: "step", Operates: operatesPatch, Scope: ScopeOwn}},
	}
	if err := CrossCheckCapabilities("frp/patch:1.0.0", EntityCapabilityClaim{}, desc); err != nil {
		t.Fatalf("empty claim should not halt; got %v", err)
	}
}
