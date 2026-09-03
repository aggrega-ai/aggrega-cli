// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"errors"
	"strings"
	"testing"
)

// TestCrossCheckContract exercises the single contract check the load gate
// and the publish path share: an entity's authored capability claim against a
// plugin's self-reported describe() descriptor.
func TestCrossCheckContract(t *testing.T) {
	t.Parallel()

	desc := PluginDescriptor{
		AbiVersion: ABIVersionV1Alpha1,
		PluginType: string(PluginTypePipelineStep),
		Exports: []PluginExport{
			{Name: "step", Operates: operatesPatch, Scope: ScopeOwn, Selector: "the plugin's own description"},
			{Name: "extra", Operates: operatesEmit, Scope: ScopeHost},
		},
	}

	cases := []struct {
		name      string
		claim     EntityContractClaim
		wantErr   bool
		wantField string // substring expected in the error message
	}{
		{
			name: "agreeing claim returns nil",
			claim: EntityContractClaim{Exports: []EntityExportClaim{
				{Name: "step", Operates: operatesPatch, Scope: ScopeOwn},
			}},
		},
		{
			name: "claim omitting an export the descriptor declares returns nil (entity-claim subset of describe)",
			claim: EntityContractClaim{Exports: []EntityExportClaim{
				{Name: "step", Operates: operatesPatch, Scope: ScopeOwn},
			}},
		},
		{
			name: "selector-only difference does not halt",
			claim: EntityContractClaim{Exports: []EntityExportClaim{
				// No selector field on the claim at all; the descriptor carries
				// one. Selector is excluded from the cross-check.
				{Name: "step", Operates: operatesPatch, Scope: ScopeOwn},
			}},
		},
		{
			name: "operates disagrees halts",
			claim: EntityContractClaim{Exports: []EntityExportClaim{
				{Name: "step", Operates: operatesEmit, Scope: ScopeOwn},
			}},
			wantErr:   true,
			wantField: "operates",
		},
		{
			name: "scope disagrees halts",
			claim: EntityContractClaim{Exports: []EntityExportClaim{
				{Name: "step", Operates: operatesPatch, Scope: ScopeHost},
			}},
			wantErr:   true,
			wantField: "scope",
		},
		{
			name: "claimed export absent from describe halts",
			claim: EntityContractClaim{Exports: []EntityExportClaim{
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
			err := CrossCheckContract("frp/patch:1.0.0", tc.claim, desc)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !errors.Is(err, ErrWasmContractMismatch()) {
				t.Fatalf("error does not wrap ErrWasmContractMismatch: %v", err)
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

// TestCrossCheckContract_PluginLevel exercises the plugin-level
// capabilities comparison: an authored capabilities claim is checked
// field-by-field against the descriptor's self-report, and a nil claim skips
// the plugin-level comparison entirely.
func TestCrossCheckContract_PluginLevel(t *testing.T) {
	t.Parallel()

	desc := PluginDescriptor{
		AbiVersion: ABIVersionV1Alpha1,
		PluginType: string(PluginTypeProvider),
		Exports:    []PluginExport{{Name: "render_node"}},
		Contract: &ProviderContract{
			InputKinds:         InputKinds{Sourced: true, Synthesized: false, ArtifactsStandalone: false},
			BoundaryAssembly:   BoundaryAssemblyNest,
			ArtifactDelimiters: &ArtifactDelimiters{Left: "[[", Right: "]]"},
		},
	}

	cases := []struct {
		name      string
		caps      *ProviderContract
		wantErr   bool
		wantField string
	}{
		{
			name: "agreeing plugin-level claim returns nil",
			caps: &ProviderContract{
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
			caps: &ProviderContract{
				InputKinds:         InputKinds{Sourced: true, Synthesized: true},
				BoundaryAssembly:   BoundaryAssemblyNest,
				ArtifactDelimiters: &ArtifactDelimiters{Left: "[[", Right: "]]"},
			},
			wantErr:   true,
			wantField: "input_kinds.synthesized",
		},
		{
			name: "boundary_assembly disagrees halts",
			caps: &ProviderContract{
				InputKinds:         InputKinds{Sourced: true},
				BoundaryAssembly:   BoundaryAssemblyMerge,
				ArtifactDelimiters: &ArtifactDelimiters{Left: "[[", Right: "]]"},
			},
			wantErr:   true,
			wantField: "boundary_assembly",
		},
		{
			name: "artifact_delimiters value disagrees halts",
			caps: &ProviderContract{
				InputKinds:         InputKinds{Sourced: true},
				BoundaryAssembly:   BoundaryAssemblyNest,
				ArtifactDelimiters: &ArtifactDelimiters{Left: "{{", Right: "}}"},
			},
			wantErr:   true,
			wantField: "artifact_delimiters",
		},
		{
			name: "artifact_delimiters presence disagrees halts",
			caps: &ProviderContract{
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
			err := CrossCheckContract("provider/helm-chart:1.0.0", EntityContractClaim{Contract: tc.caps}, desc)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !errors.Is(err, ErrWasmContractMismatch()) {
				t.Fatalf("error does not wrap ErrWasmContractMismatch: %v", err)
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

// TestCrossCheckContract_AttachmentSegment exercises the attachment-directory arm
// of the plugin-level comparison, whose claim carries three states rather than
// two: a named directory, an explicitly empty one, and no declaration.
//
// The comparison is on declaredness first and then on the directory named, so the
// cases are enumerated as the grid rather than as a list — a per-axis pair would
// specify the margins and leave the interior unstated, and the interior is where
// the two empty-ish states meet.
//
// 🔴 The row that matters most is the last: a claim and a report that BOTH name
// the empty string must AGREE. Comparing the two pointers directly type-checks and
// reads like a match while testing identity, so that row fails on a pointer
// comparison and passes on a value one. It is the reason this test exists.
func TestCrossCheckContract_AttachmentSegment(t *testing.T) {
	t.Parallel()

	// declares builds the carrier a contract naming segment holds. A nil carrier
	// declares nothing, and the two are spelled differently at every row because
	// declares("") is a claim about layout rather than the absence of one.
	declares := func(segment string) *string { return &segment }

	descWith := func(segment *string) PluginDescriptor {
		return PluginDescriptor{
			AbiVersion: ABIVersionV1Alpha1,
			PluginType: string(PluginTypeProvider),
			Exports:    []PluginExport{{Name: "render_node"}},
			Contract: &ProviderContract{
				InputKinds:        InputKinds{Sourced: true},
				BoundaryAssembly:  BoundaryAssemblyNone,
				AttachmentSegment: segment,
			},
		}
	}
	capsWith := func(segment *string) *ProviderContract {
		return &ProviderContract{
			InputKinds:        InputKinds{Sourced: true},
			BoundaryAssembly:  BoundaryAssemblyNone,
			AttachmentSegment: segment,
		}
	}

	cases := []struct {
		name      string
		entity    *string
		plugin    *string
		wantErr   bool
		wantField string
	}{
		{
			name:   "neither declares a directory: match",
			entity: nil,
			plugin: nil,
		},
		{
			name:   "both name the same directory: match",
			entity: declares("addons"),
			plugin: declares("addons"),
		},
		{
			name:      "both name a directory but they differ: halt",
			entity:    declares("addons"),
			plugin:    declares("charts"),
			wantErr:   true,
			wantField: "attachment_segment",
		},
		{
			name:      "entity names a directory, plugin declares none: halt",
			entity:    declares("addons"),
			plugin:    nil,
			wantErr:   true,
			wantField: "attachment_segment",
		},
		{
			name:      "plugin names a directory, entity declares none: halt",
			entity:    nil,
			plugin:    declares("addons"),
			wantErr:   true,
			wantField: "attachment_segment",
		},
		{
			// A genuine disagreement between two reachable states: the plugin
			// declares that its target wants no enclosing directory while the
			// entity declares nothing at all. The halt says so in terms of whether
			// a directory was declared rather than by quoting two directory names
			// the author would read as identical.
			name:      "plugin declares no enclosing directory, entity declares nothing: halt",
			entity:    nil,
			plugin:    declares(""),
			wantErr:   true,
			wantField: "whether attachment_segment is declared",
		},
		{
			// The row a pointer comparison fails. Two separately built carriers
			// naming the same empty directory are equal in content and distinct in
			// identity, so this passes only where the comparison dereferences.
			name:   "both declare no enclosing directory: match",
			entity: declares(""),
			plugin: declares(""),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := CrossCheckContract(
				"provider/helm:1.0.0",
				EntityContractClaim{Contract: capsWith(tc.entity)},
				descWith(tc.plugin),
			)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !errors.Is(err, ErrWasmContractMismatch()) {
				t.Fatalf("error does not wrap ErrWasmContractMismatch: %v", err)
			}
			if !strings.Contains(err.Error(), "provider/helm:1.0.0") {
				t.Errorf("error should name the entity identity; got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantField) {
				t.Errorf("error should mention %q; got: %v", tc.wantField, err)
			}
		})
	}
}

// TestCrossCheckContract_CrossBoundaryLinking exercises the cross-boundary
// linking arm of the plugin-level comparison: the entity's authored
// crossBoundaryLinking claim is checked against the descriptor's self-reported
// cross_boundary_linking flag, and a disagreement in either direction halts.
func TestCrossCheckContract_CrossBoundaryLinking(t *testing.T) {
	t.Parallel()

	// A descriptor that advertises cross-boundary linking exports both link
	// exports and reports the flag; the crosscheck compares the flag directly.
	linkingDesc := PluginDescriptor{
		AbiVersion: ABIVersionV1Alpha1,
		PluginType: string(PluginTypeProvider),
		Exports: []PluginExport{
			{Name: "render_node"},
			{Name: LinkPublishExport},
			{Name: LinkResolveExport},
		},
		Contract: &ProviderContract{
			InputKinds:           InputKinds{Sourced: true},
			BoundaryAssembly:     BoundaryAssemblyMerge,
			CrossBoundaryLinking: true,
		},
	}
	// A descriptor that wires no cross-boundary edges reports the flag false.
	nonLinkingDesc := PluginDescriptor{
		AbiVersion: ABIVersionV1Alpha1,
		PluginType: string(PluginTypeProvider),
		Exports:    []PluginExport{{Name: "render_node"}},
		Contract: &ProviderContract{
			InputKinds:       InputKinds{Sourced: true},
			BoundaryAssembly: BoundaryAssemblyMerge,
		},
	}

	cases := []struct {
		name      string
		desc      PluginDescriptor
		caps      *ProviderContract
		wantErr   bool
		wantField string
	}{
		{
			name: "entity claims linking, descriptor advertises it: match",
			desc: linkingDesc,
			caps: &ProviderContract{
				InputKinds:           InputKinds{Sourced: true},
				BoundaryAssembly:     BoundaryAssemblyMerge,
				CrossBoundaryLinking: true,
			},
		},
		{
			name: "entity claims linking, descriptor does not advertise it: halt",
			desc: nonLinkingDesc,
			caps: &ProviderContract{
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
			caps: &ProviderContract{
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
			err := CrossCheckContract("provider/cfn-renderer:1.0.0", EntityContractClaim{Contract: tc.caps}, tc.desc)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !errors.Is(err, ErrWasmContractMismatch()) {
				t.Fatalf("error does not wrap ErrWasmContractMismatch: %v", err)
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

// TestCrossCheckContract_EmptyClaim proves an entity with no authored
// exports declares no claim and never halts, regardless of what the plugin
// reports.
func TestCrossCheckContract_EmptyClaim(t *testing.T) {
	t.Parallel()
	desc := PluginDescriptor{
		AbiVersion: ABIVersionV1Alpha1,
		PluginType: string(PluginTypePipelineStep),
		Exports:    []PluginExport{{Name: "step", Operates: operatesPatch, Scope: ScopeOwn}},
	}
	if err := CrossCheckContract("frp/patch:1.0.0", EntityContractClaim{}, desc); err != nil {
		t.Fatalf("empty claim should not halt; got %v", err)
	}
}
