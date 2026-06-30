// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestPluginDescriptorRoundTrip(t *testing.T) {
	t.Parallel()

	cases := map[string]PluginDescriptor{
		"provider-helm-chart": {
			ABIVersion: ABIVersionV1Alpha1,
			PluginType: PluginTypeProvider,
			Exports: []PluginExport{
				{Name: "render_node", InputPattern: InputPatternFull, Stability: StabilityStable},
				{Name: "assemble_boundary", InputPattern: InputPatternFull, Stability: StabilityStable},
			},
			Metadata: PluginDescriptorMeta{Author: "aggrega", License: "Apache-2.0"},
		},
		"provider-helm": {
			ABIVersion: ABIVersionV1Alpha1,
			PluginType: PluginTypeProvider,
			Exports: []PluginExport{
				{Name: "render_node", InputPattern: InputPatternFull},
			},
		},
		"linter-multi-rule": {
			ABIVersion: ABIVersionV1Alpha1,
			PluginType: PluginTypeLinter,
			Exports: []PluginExport{
				{Name: "lint_naming", HostFunctions: []string{"aggrega_emit_warning"}},
				{Name: "lint_dependencies"},
				{Name: "lint_annotations", HostFunctions: []string{"aggrega_log"}},
			},
		},
		"frp-step": {
			ABIVersion: ABIVersionV1Alpha1,
			PluginType: PluginTypeFRPStep,
			Exports: []PluginExport{
				{Name: "step", InputPattern: InputPatternMinimal},
			},
		},
		"frp-step-with-capabilities": {
			ABIVersion:          ABIVersionV1Alpha1,
			PluginType:          PluginTypeFRPStep,
			CompatibleProviders: []string{"helm", "helm-chart"},
			Exports: []PluginExport{
				{
					Name:         "step",
					InputPattern: InputPatternMinimal,
					Operates:     "patch",
					Scope:        "host",
					Selector:     "workloads carrying a pod template",
				},
			},
		},
	}

	for name, want := range cases {
		want := want
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			data, err := json.Marshal(want)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var got PluginDescriptor
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("round-trip mismatch:\n want %#v\n got  %#v", want, got)
			}
			// Each descriptor must pass Validate on its own.
			if err := want.Validate(name); err != nil {
				t.Fatalf("Validate(%s): %v", name, err)
			}
		})
	}
}

func TestPluginDescriptorMarshalDeterministic(t *testing.T) {
	t.Parallel()

	d := PluginDescriptor{
		ABIVersion: ABIVersionV1Alpha1,
		PluginType: PluginTypeLinter,
		Exports: []PluginExport{
			{Name: "lint_zeta", HostFunctions: []string{"aggrega_log", "aggrega_emit_warning"}},
			{Name: "lint_alpha"},
		},
	}

	first, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	second, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("non-deterministic marshal:\n %s\n %s", first, second)
	}

	// Exports preserve declared order (lint_zeta before lint_alpha), not
	// an alphabetical sort, and host_functions preserve their declared
	// slice order.
	var got PluginDescriptor
	if err := json.Unmarshal(first, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Exports[0].Name != "lint_zeta" || got.Exports[1].Name != "lint_alpha" {
		t.Fatalf("export order not preserved: %v", got.Exports)
	}
	if !reflect.DeepEqual(got.Exports[0].HostFunctions, []string{"aggrega_log", "aggrega_emit_warning"}) {
		t.Fatalf("host_functions order not preserved: %v", got.Exports[0].HostFunctions)
	}
}

func TestPluginDescriptorValidateABIHandshake(t *testing.T) {
	t.Parallel()

	base := func() PluginDescriptor {
		return PluginDescriptor{
			PluginType: PluginTypeProvider,
			Exports:    []PluginExport{{Name: "render_node"}},
		}
	}

	for _, bad := range []string{"", "v1", "v1beta1", "v2alpha1"} {
		d := base()
		d.ABIVersion = bad
		err := d.Validate("acme/provider")
		if !errors.Is(err, ErrWasmManifestRejected) {
			t.Fatalf("abi_version %q: want ErrWasmManifestRejected, got %v", bad, err)
		}
	}

	d := base()
	d.ABIVersion = ABIVersionV1Alpha1
	if err := d.Validate("acme/provider"); err != nil {
		t.Fatalf("abi_version %q: want accept, got %v", ABIVersionV1Alpha1, err)
	}
}

func TestPluginDescriptorValidateUnknownPluginType(t *testing.T) {
	t.Parallel()

	d := PluginDescriptor{
		ABIVersion: ABIVersionV1Alpha1,
		PluginType: PluginType("transmogrifier"),
		Exports:    []PluginExport{{Name: "render_node"}},
	}
	err := d.Validate("acme/mystery")
	if !errors.Is(err, ErrWasmManifestRejected) {
		t.Fatalf("unknown plugin_type: want ErrWasmManifestRejected, got %v", err)
	}
}

func TestPluginDescriptorValidateMinimumExports(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		descriptor PluginDescriptor
		wantReject bool
	}{
		"provider-without-render_node": {
			descriptor: PluginDescriptor{
				ABIVersion: ABIVersionV1Alpha1,
				PluginType: PluginTypeProvider,
				Exports:    []PluginExport{{Name: "assemble_boundary"}},
			},
			wantReject: true,
		},
		"provider-with-render_node": {
			descriptor: PluginDescriptor{
				ABIVersion: ABIVersionV1Alpha1,
				PluginType: PluginTypeProvider,
				Exports:    []PluginExport{{Name: "render_node"}},
			},
		},
		"linter-without-lint": {
			descriptor: PluginDescriptor{
				ABIVersion: ABIVersionV1Alpha1,
				PluginType: PluginTypeLinter,
				Exports:    []PluginExport{{Name: "describe_rules"}},
			},
			wantReject: true,
		},
		"linter-with-lint": {
			descriptor: PluginDescriptor{
				ABIVersion: ABIVersionV1Alpha1,
				PluginType: PluginTypeLinter,
				Exports:    []PluginExport{{Name: "lint_naming"}},
			},
		},
		"frp_step-without-step": {
			descriptor: PluginDescriptor{
				ABIVersion: ABIVersionV1Alpha1,
				PluginType: PluginTypeFRPStep,
				Exports:    []PluginExport{{Name: "transform"}},
			},
			wantReject: true,
		},
		"frp_step-with-step": {
			descriptor: PluginDescriptor{
				ABIVersion: ABIVersionV1Alpha1,
				PluginType: PluginTypeFRPStep,
				Exports:    []PluginExport{{Name: "step"}},
			},
		},
	}

	for name, tc := range cases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := tc.descriptor.Validate("acme/plugin")
			if tc.wantReject {
				if !errors.Is(err, ErrWasmManifestRejected) {
					t.Fatalf("want ErrWasmManifestRejected, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("want accept, got %v", err)
			}
		})
	}
}

func TestPluginDescriptorValidateHostFunctionsSubsetOfAllowlist(t *testing.T) {
	t.Parallel()

	// A descriptor declaring an un-allowlisted host function is rejected.
	// This exercises the real loader gate (validateHostFunctionsSubset),
	// not a stub: the only bindable host functions in v1alpha1 are the two
	// baseline diagnostics, so a catalog_lookup declaration is refused.
	reject := PluginDescriptor{
		ABIVersion: ABIVersionV1Alpha1,
		PluginType: PluginTypeProvider,
		Exports: []PluginExport{
			{Name: "render_node", HostFunctions: []string{"aggrega_log", "catalog_lookup"}},
		},
	}
	if err := reject.Validate("acme/provider"); !errors.Is(err, ErrWasmManifestRejected) {
		t.Fatalf("un-allowlisted host function: want ErrWasmManifestRejected, got %v", err)
	}

	// A descriptor declaring only baseline host functions passes.
	accept := PluginDescriptor{
		ABIVersion: ABIVersionV1Alpha1,
		PluginType: PluginTypeProvider,
		Exports: []PluginExport{
			{Name: "render_node", HostFunctions: []string{"aggrega_log", "aggrega_emit_warning"}},
		},
	}
	if err := accept.Validate("acme/provider"); err != nil {
		t.Fatalf("baseline host functions: want accept, got %v", err)
	}
}

// TestPluginDescriptorValidateCapabilityShape exercises the shape-only
// capability validation: operates must be patch, emit, or empty; scope
// must be own, host, or empty; selector is free-form. A malformed value
// is rejected; a well-shaped or absent value passes. This validator does
// NOT check provider compatibility, require a capability per plugin type,
// or consult a provider registry — that enforcement is the runtime's.
func TestPluginDescriptorValidateCapabilityShape(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		export     PluginExport
		wantReject bool
	}{
		"capabilities-absent": {
			export: PluginExport{Name: "step"},
		},
		"operates-patch-scope-own": {
			export: PluginExport{Name: "step", Operates: "patch", Scope: "own"},
		},
		"operates-emit-scope-host": {
			export: PluginExport{Name: "step", Operates: "emit", Scope: "host"},
		},
		"selector-free-form": {
			export: PluginExport{Name: "step", Selector: "anything at all is fine here"},
		},
		"operates-malformed": {
			export:     PluginExport{Name: "step", Operates: "mutate"},
			wantReject: true,
		},
		"scope-malformed": {
			export:     PluginExport{Name: "step", Scope: "global"},
			wantReject: true,
		},
	}

	for name, tc := range cases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := PluginDescriptor{
				ABIVersion: ABIVersionV1Alpha1,
				PluginType: PluginTypeFRPStep,
				Exports:    []PluginExport{tc.export},
			}
			// The frp_step minimum export is "step"; every case names it so
			// only the capability-shape check is under test.
			err := d.Validate("acme/frp-step")
			if tc.wantReject {
				if !errors.Is(err, ErrWasmManifestRejected) {
					t.Fatalf("want ErrWasmManifestRejected, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("want accept, got %v", err)
			}
		})
	}
}

// TestPluginDescriptorCompatibleProvidersUnchecked proves compatible_providers
// is piped through at load without a registry check: an arbitrary,
// never-heard-of provider name is accepted here. The runtime — not this
// load-time validator — enforces compatibility.
func TestPluginDescriptorCompatibleProvidersUnchecked(t *testing.T) {
	t.Parallel()
	d := PluginDescriptor{
		ABIVersion:          ABIVersionV1Alpha1,
		PluginType:          PluginTypeFRPStep,
		CompatibleProviders: []string{"a-provider-that-does-not-exist", "another"},
		Exports:             []PluginExport{{Name: "step"}},
	}
	if err := d.Validate("acme/frp-step"); err != nil {
		t.Fatalf("compatible_providers must be unchecked at load, got %v", err)
	}
}

func TestDefaultInputPattern(t *testing.T) {
	t.Parallel()

	cases := []struct {
		pluginType PluginType
		exportName string
		want       InputPattern
	}{
		{PluginTypeProvider, "render_node", InputPatternFull},
		{PluginTypeProvider, "assemble_boundary", InputPatternFull},
		{PluginTypeLinter, "lint_naming", InputPatternFull},
		{PluginTypeFRPStep, "step", InputPatternMinimal},
	}
	for _, tc := range cases {
		if got := defaultInputPattern(tc.pluginType, tc.exportName); got != tc.want {
			t.Fatalf("defaultInputPattern(%s, %s) = %q, want %q",
				tc.pluginType, tc.exportName, got, tc.want)
		}
	}
}

// TestHostFunctionAllowlistUnchanged pins the v1alpha1 host-function
// surface to exactly the two baseline diagnostic functions. A change
// that adds a host function without a corresponding decision fails here
// loudly rather than silently widening the bound surface.
func TestHostFunctionAllowlistUnchanged(t *testing.T) {
	t.Parallel()

	want := map[string]bool{
		"aggrega_log":          true,
		"aggrega_emit_warning": true,
	}
	if !reflect.DeepEqual(hostFunctionAllowlist, want) {
		t.Fatalf("hostFunctionAllowlist changed: got %v, want %v", hostFunctionAllowlist, want)
	}
}
