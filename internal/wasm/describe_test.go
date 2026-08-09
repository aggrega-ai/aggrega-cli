// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestPluginDescriptorRoundTrip(t *testing.T) {
	t.Parallel()

	cases := map[string]PluginDescriptor{
		"provider-helm-chart": {
			AbiVersion: ABIVersionV1Alpha1,
			PluginType: string(PluginTypeProvider),
			Exports: []PluginExport{
				{Name: "render_node", InputPattern: string(InputPatternFull), Stability: string(StabilityStable)},
				{Name: "assemble_boundary", InputPattern: string(InputPatternFull), Stability: string(StabilityStable)},
			},
			Metadata: &PluginDescriptorMeta{Author: "aggrega", License: "Apache-2.0"},
		},
		"provider-helm": {
			AbiVersion: ABIVersionV1Alpha1,
			PluginType: string(PluginTypeProvider),
			Exports: []PluginExport{
				{Name: "render_node", InputPattern: string(InputPatternFull)},
			},
		},
		"linter-multi-rule": {
			AbiVersion: ABIVersionV1Alpha1,
			PluginType: string(PluginTypeLinter),
			Exports: []PluginExport{
				{Name: "lint_naming", HostFunctions: []string{"aggrega_emit_warning"}},
				{Name: "lint_dependencies"},
				{Name: "lint_annotations", HostFunctions: []string{"aggrega_log"}},
			},
		},
		"frp-step": {
			AbiVersion: ABIVersionV1Alpha1,
			PluginType: string(PluginTypePipelineStep),
			Exports: []PluginExport{
				{Name: "step", InputPattern: string(InputPatternMinimal)},
			},
		},
		"frp-step-with-capabilities": {
			AbiVersion:   ABIVersionV1Alpha1,
			PluginType:   string(PluginTypePipelineStep),
			Capabilities: []string{"helm", "helm-chart"},
			Exports: []PluginExport{
				{
					Name:         "step",
					InputPattern: string(InputPatternMinimal),
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
		AbiVersion: ABIVersionV1Alpha1,
		PluginType: string(PluginTypeLinter),
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
			PluginType: string(PluginTypeProvider),
			Exports:    []PluginExport{{Name: "render_node"}},
		}
	}

	for _, bad := range []string{"", "v1", "v1beta1", "v2alpha1"} {
		d := base()
		d.AbiVersion = bad
		err := d.Validate("acme/provider")
		if !errors.Is(err, ErrWasmManifestRejected()) {
			t.Fatalf("abi_version %q: want ErrWasmManifestRejected, got %v", bad, err)
		}
	}

	d := base()
	d.AbiVersion = ABIVersionV1Alpha1
	if err := d.Validate("acme/provider"); err != nil {
		t.Fatalf("abi_version %q: want accept, got %v", ABIVersionV1Alpha1, err)
	}
}

// TestPluginDescriptorValidateUnknownPluginType pins the rejection a descriptor
// gets when its plugin_type is not one the registry recognizes: the wrapped
// sentinel and the exact message text, both of which reach a user's terminal.
// The message is asserted in full rather than probed for a substring, because the
// plugin-type check reads its type set from the registry and the identity of that
// rejection must not drift with the registry's internals.
func TestPluginDescriptorValidateUnknownPluginType(t *testing.T) {
	t.Parallel()

	d := PluginDescriptor{
		AbiVersion: ABIVersionV1Alpha1,
		PluginType: string(PluginType("transmogrifier")),
		Exports:    []PluginExport{{Name: "render_node"}},
	}
	err := d.Validate("acme/mystery")
	if !errors.Is(err, ErrWasmManifestRejected()) {
		t.Fatalf("unknown plugin_type: want ErrWasmManifestRejected, got %v", err)
	}
	const want = `plugin "acme/mystery" declares unknown plugin_type "transmogrifier": ` +
		"wasm: plugin rejected by the sandbox"
	if got := err.Error(); got != want {
		t.Fatalf("unknown plugin_type message:\n got: %s\nwant: %s", got, want)
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
				AbiVersion: ABIVersionV1Alpha1,
				PluginType: string(PluginTypeProvider),
				Exports:    []PluginExport{{Name: "assemble_boundary"}},
			},
			wantReject: true,
		},
		"provider-with-render_node": {
			descriptor: PluginDescriptor{
				AbiVersion: ABIVersionV1Alpha1,
				PluginType: string(PluginTypeProvider),
				Exports:    []PluginExport{{Name: "render_node"}},
			},
		},
		"linter-without-lint": {
			descriptor: PluginDescriptor{
				AbiVersion: ABIVersionV1Alpha1,
				PluginType: string(PluginTypeLinter),
				Exports:    []PluginExport{{Name: "describe_rules"}},
			},
			wantReject: true,
		},
		"linter-with-lint": {
			descriptor: PluginDescriptor{
				AbiVersion: ABIVersionV1Alpha1,
				PluginType: string(PluginTypeLinter),
				Exports:    []PluginExport{{Name: "lint_naming"}},
			},
		},
		"pipeline-step-without-step": {
			descriptor: PluginDescriptor{
				AbiVersion: ABIVersionV1Alpha1,
				PluginType: string(PluginTypePipelineStep),
				Exports:    []PluginExport{{Name: "transform"}},
			},
			wantReject: true,
		},
		"pipeline-step-with-step": {
			descriptor: PluginDescriptor{
				AbiVersion: ABIVersionV1Alpha1,
				PluginType: string(PluginTypePipelineStep),
				Exports:    []PluginExport{{Name: "step"}},
			},
		},
		"cli-without-cli_run": {
			descriptor: PluginDescriptor{
				AbiVersion: ABIVersionV1Alpha1,
				PluginType: string(PluginTypeCLI),
				Exports:    []PluginExport{{Name: "run"}},
			},
			wantReject: true,
		},
		"cli-with-cli_run": {
			descriptor: PluginDescriptor{
				AbiVersion: ABIVersionV1Alpha1,
				PluginType: string(PluginTypeCLI),
				Exports:    []PluginExport{{Name: "cli_run"}},
			},
		},
		"parser-without-parse": {
			descriptor: PluginDescriptor{
				AbiVersion:   ABIVersionV1Alpha1,
				PluginType:   string(PluginTypeParser),
				Capabilities: []string{"parse:hcl"},
				Exports:      []PluginExport{{Name: "read"}},
			},
			wantReject: true,
		},
		"parser-with-parse": {
			descriptor: PluginDescriptor{
				AbiVersion:   ABIVersionV1Alpha1,
				PluginType:   string(PluginTypeParser),
				Capabilities: []string{"parse:hcl", "parse:toml"},
				Exports:      []PluginExport{{Name: "parse"}},
			},
		},
	}

	for name, tc := range cases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := tc.descriptor.Validate("acme/plugin")
			if tc.wantReject {
				if !errors.Is(err, ErrWasmManifestRejected()) {
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

func TestPluginDescriptorValidateCLI(t *testing.T) {
	t.Parallel()

	// A scratch cli descriptor with the cli_run export passes Validate.
	withRun := PluginDescriptor{
		AbiVersion: ABIVersionV1Alpha1,
		PluginType: string(PluginTypeCLI),
		Exports:    []PluginExport{{Name: CLIRunExportName}},
	}
	if err := withRun.Validate("acme/cli"); err != nil {
		t.Fatalf("cli with %q: want accept, got %v", CLIRunExportName, err)
	}

	// The same descriptor without cli_run fails, wrapping ErrWasmManifestRejected.
	withoutRun := PluginDescriptor{
		AbiVersion: ABIVersionV1Alpha1,
		PluginType: string(PluginTypeCLI),
		Exports:    []PluginExport{{Name: "run"}},
	}
	if err := withoutRun.Validate("acme/cli"); !errors.Is(err, ErrWasmManifestRejected()) {
		t.Fatalf("cli without %q: want ErrWasmManifestRejected, got %v", CLIRunExportName, err)
	}
}

// TestPluginDescriptorValidateCommands proves the commands shape check: a CLI
// plugin declaring several well-named commands validates, an empty commands list
// validates (command presence is enforced at install, not here), and a command
// with an empty name is refused wrapping ErrWasmManifestRejected.
func TestPluginDescriptorValidateCommands(t *testing.T) {
	t.Parallel()

	multi := PluginDescriptor{
		AbiVersion: ABIVersionV1Alpha1,
		PluginType: string(PluginTypeCLI),
		Commands:   []PluginCLICommand{{Name: "greet-a", Description: "A"}, {Name: "greet-b"}},
		Exports:    []PluginExport{{Name: CLIRunExportName}},
	}
	if err := multi.Validate("acme/cli"); err != nil {
		t.Fatalf("cli with several well-named commands: want accept, got %v", err)
	}

	none := PluginDescriptor{
		AbiVersion: ABIVersionV1Alpha1,
		PluginType: string(PluginTypeCLI),
		Exports:    []PluginExport{{Name: CLIRunExportName}},
	}
	if err := none.Validate("acme/cli"); err != nil {
		t.Fatalf("cli with no commands must pass the load gate (presence is an install concern): %v", err)
	}

	emptyName := PluginDescriptor{
		AbiVersion: ABIVersionV1Alpha1,
		PluginType: string(PluginTypeCLI),
		Commands:   []PluginCLICommand{{Name: "ok"}, {Name: ""}},
		Exports:    []PluginExport{{Name: CLIRunExportName}},
	}
	if err := emptyName.Validate("acme/cli"); !errors.Is(err, ErrWasmManifestRejected()) {
		t.Fatalf("cli with an empty command name: want ErrWasmManifestRejected, got %v", err)
	}
}

// TestPluginDescriptorValidateSettingsSchemaShape checks the load-time shape gate
// on a declared settings_schema: a well-formed schema passes, an absent schema
// passes, and a malformed schema document fails the load wrapping
// ErrWasmManifestRejected and naming the plugin.
func TestPluginDescriptorValidateSettingsSchemaShape(t *testing.T) {
	t.Parallel()

	base := func(schema string) PluginDescriptor {
		return PluginDescriptor{
			AbiVersion:     ABIVersionV1Alpha1,
			PluginType:     string(PluginTypeGovernance),
			Exports:        []PluginExport{{Name: ValidateExport}},
			SettingsSchema: json.RawMessage(schema),
		}
	}

	if err := base("").Validate("acme/policy"); err != nil {
		t.Fatalf("absent settings_schema must pass the load gate: %v", err)
	}

	wellFormed := base(`{"type":"object","properties":{"max_replicas":{"type":"integer"}},"required":["max_replicas"]}`)
	if err := wellFormed.Validate("acme/policy"); err != nil {
		t.Fatalf("well-formed settings_schema must pass the load gate: %v", err)
	}

	// A schema whose "type" is not a valid JSON Schema type value does not
	// compile, so the load gate refuses it.
	malformed := base(`{"type":123}`)
	err := malformed.Validate("acme/policy")
	if !errors.Is(err, ErrWasmManifestRejected()) {
		t.Fatalf("malformed settings_schema: want ErrWasmManifestRejected, got %v", err)
	}
	if !strings.Contains(err.Error(), "acme/policy") {
		t.Errorf("malformed settings_schema rejection does not name the plugin: %v", err)
	}
}

// TestPluginDescriptorValidateSettings checks the dispatch-time settings
// validation: a descriptor with no schema accepts any settings (free-form
// pass-through), and a descriptor with a schema accepts conforming settings and
// rejects non-conforming ones with a message naming the offending key.
func TestPluginDescriptorValidateSettings(t *testing.T) {
	t.Parallel()

	noSchema := PluginDescriptor{AbiVersion: ABIVersionV1Alpha1, PluginType: string(PluginTypeGovernance)}
	if err := noSchema.ValidateSettings(map[string]any{"whatever": 1}); err != nil {
		t.Fatalf("no schema must accept any settings (free-form): %v", err)
	}
	if err := noSchema.ValidateSettings(nil); err != nil {
		t.Fatalf("no schema must accept nil settings: %v", err)
	}

	typed := PluginDescriptor{
		AbiVersion:     ABIVersionV1Alpha1,
		PluginType:     string(PluginTypeGovernance),
		SettingsSchema: json.RawMessage(`{"type":"object","properties":{"max_replicas":{"type":"integer"}},"required":["max_replicas"],"additionalProperties":false}`),
	}
	if err := typed.ValidateSettings(map[string]any{"max_replicas": 3}); err != nil {
		t.Fatalf("conforming settings must pass: %v", err)
	}
	if err := typed.ValidateSettings(map[string]any{"max_replicas": "three"}); err == nil {
		t.Errorf("wrong-typed setting must be rejected")
	} else if !strings.Contains(err.Error(), "max_replicas") {
		t.Errorf("rejection does not name the offending key: %v", err)
	}
	if err := typed.ValidateSettings(nil); err == nil {
		t.Errorf("absent settings must be rejected when the schema requires a key")
	}
}

func TestPluginDescriptorPluginLevelDescriptionRoundTrip(t *testing.T) {
	t.Parallel()

	want := PluginDescriptor{
		AbiVersion:  ABIVersionV1Alpha1,
		PluginType:  string(PluginTypeCLI),
		Description: "seeds a new Aggregate scaffold under the current directory",
		Exports:     []PluginExport{{Name: CLIRunExportName}},
	}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got PluginDescriptor
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Description != want.Description {
		t.Fatalf("plugin-level description not preserved: want %q, got %q", want.Description, got.Description)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("round-trip mismatch:\n want %#v\n got  %#v", want, got)
	}

	// An omitted plugin-level description marshals no "description" key, so
	// existing guests and cached descriptors that omit it stay byte-stable.
	none := PluginDescriptor{
		AbiVersion: ABIVersionV1Alpha1,
		PluginType: string(PluginTypeCLI),
		Exports:    []PluginExport{{Name: CLIRunExportName}},
	}
	noneData, err := json.Marshal(none)
	if err != nil {
		t.Fatalf("marshal (no description): %v", err)
	}
	if bytes.Contains(noneData, []byte("description")) {
		t.Fatalf("omitted plugin-level description leaked a key: %s", noneData)
	}
}

func TestPluginDescriptorValidateHostFunctionsSubsetOfAllowlist(t *testing.T) {
	t.Parallel()

	// A descriptor declaring an un-allowlisted host function is rejected.
	// This exercises the real loader gate (validateHostFunctionsSubset),
	// not a stub: the only bindable host functions in v1alpha1 are the two
	// baseline diagnostics, so a catalog_lookup declaration is refused.
	reject := PluginDescriptor{
		AbiVersion: ABIVersionV1Alpha1,
		PluginType: string(PluginTypeProvider),
		Exports: []PluginExport{
			{Name: "render_node", HostFunctions: []string{"aggrega_log", "catalog_lookup"}},
		},
	}
	if err := reject.Validate("acme/provider"); !errors.Is(err, ErrWasmManifestRejected()) {
		t.Fatalf("un-allowlisted host function: want ErrWasmManifestRejected, got %v", err)
	}

	// A descriptor declaring only baseline host functions passes.
	accept := PluginDescriptor{
		AbiVersion: ABIVersionV1Alpha1,
		PluginType: string(PluginTypeProvider),
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
				AbiVersion: ABIVersionV1Alpha1,
				PluginType: string(PluginTypePipelineStep),
				Exports:    []PluginExport{tc.export},
			}
			// The pipeline-step minimum export is "step"; every case names it so
			// only the capability-shape check is under test.
			err := d.Validate("acme/frp-step")
			if tc.wantReject {
				if !errors.Is(err, ErrWasmManifestRejected()) {
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

// TestPluginDescriptorTargetsUnchecked proves capabilities
// is piped through at load without a registry check: an arbitrary,
// never-heard-of provider name is accepted here. The runtime — not this
// load-time validator — enforces compatibility.
func TestPluginDescriptorTargetsUnchecked(t *testing.T) {
	t.Parallel()
	d := PluginDescriptor{
		AbiVersion:   ABIVersionV1Alpha1,
		PluginType:   string(PluginTypePipelineStep),
		Capabilities: []string{"a-provider-that-does-not-exist", "another"},
		Exports:      []PluginExport{{Name: "step"}},
	}
	if err := d.Validate("acme/frp-step"); err != nil {
		t.Fatalf("capabilities must be unchecked at load, got %v", err)
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
		{PluginTypePipelineStep, "step", InputPatternMinimal},
	}
	for _, tc := range cases {
		if got := defaultInputPattern(tc.pluginType, tc.exportName); got != tc.want {
			t.Fatalf("defaultInputPattern(%s, %s) = %q, want %q",
				tc.pluginType, tc.exportName, got, tc.want)
		}
	}
}

// TestPluginDescriptorValidateLinkCapability exercises the cross-boundary
// linking coherence rule: the cross_boundary_linking capability flag is set if
// and only if the descriptor declares both the link_publish and link_resolve
// exports. A flag without both exports, or both exports without the flag, is
// refused at load; a coherent pair (both present, or neither) is accepted. The
// provider render_node minimum export is present in every case so only the
// link-coherence check is under test.
func TestPluginDescriptorValidateLinkCapability(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		exports    []PluginExport
		linking    bool
		wantReject bool
	}{
		"both-exports-and-flag-accept": {
			exports: []PluginExport{
				{Name: "render_node"},
				{Name: LinkPublishExport},
				{Name: LinkResolveExport},
			},
			linking: true,
		},
		"neither-export-nor-flag-accept": {
			exports: []PluginExport{
				{Name: "render_node"},
			},
			linking: false,
		},
		"flag-without-either-export-reject": {
			exports: []PluginExport{
				{Name: "render_node"},
			},
			linking:    true,
			wantReject: true,
		},
		"flag-with-only-publish-reject": {
			exports: []PluginExport{
				{Name: "render_node"},
				{Name: LinkPublishExport},
			},
			linking:    true,
			wantReject: true,
		},
		"flag-with-only-resolve-reject": {
			exports: []PluginExport{
				{Name: "render_node"},
				{Name: LinkResolveExport},
			},
			linking:    true,
			wantReject: true,
		},
		"both-exports-without-flag-reject": {
			exports: []PluginExport{
				{Name: "render_node"},
				{Name: LinkPublishExport},
				{Name: LinkResolveExport},
			},
			linking:    false,
			wantReject: true,
		},
	}

	for name, tc := range cases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := PluginDescriptor{
				AbiVersion: ABIVersionV1Alpha1,
				PluginType: string(PluginTypeProvider),
				Exports:    tc.exports,
				Contract:   &ProviderContract{CrossBoundaryLinking: tc.linking},
			}
			err := d.Validate("acme/link-provider")
			if tc.wantReject {
				if !errors.Is(err, ErrWasmManifestRejected()) {
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

// TestValidate_StreamingHostFuncs_DescriptorLevelDisjointness locks the
// plugin-type-aware split in the descriptor host-function gate: the CLI
// streaming emitters (aggrega_cli_write_stdout / _stderr) are admissible only
// for the cli plugin type. Every compile-time plugin type runs under the build
// profile and must reject a descriptor that declares a streaming emitter, exactly
// as the import-level gate rejects a build-profile guest that imports one; only
// the cli type, running under the runtime terminal profile, admits it. Both cases
// go through the real gate, PluginDescriptor.Validate, so the disjointness is
// proven at the descriptor boundary rather than through a private helper.
func TestValidate_StreamingHostFuncs_DescriptorLevelDisjointness(t *testing.T) {
	t.Parallel()

	// A build-profile plugin type (provider) that declares the streaming
	// emitter is refused: render_node satisfies the minimum-export rule, and a
	// second export declaring aggrega_cli_write_stdout is checked against the
	// shared gate, which does not admit the streaming name.
	nonCLI := PluginDescriptor{
		AbiVersion: ABIVersionV1Alpha1,
		PluginType: string(PluginTypeProvider),
		Exports: []PluginExport{
			{Name: "render_node"},
			{Name: "emit_progress", HostFunctions: []string{"aggrega_cli_write_stdout"}},
		},
	}
	if err := nonCLI.Validate("acme/provider"); !errors.Is(err, ErrWasmManifestRejected()) {
		t.Fatalf("non-cli type declaring a streaming host function: want ErrWasmManifestRejected, got %v", err)
	}

	// A cli plugin type declaring the same streaming emitter on its cli_run
	// export is accepted: the cli type routes through the CLI union gate, which
	// admits the streaming name.
	cli := PluginDescriptor{
		AbiVersion: ABIVersionV1Alpha1,
		PluginType: string(PluginTypeCLI),
		Exports: []PluginExport{
			{Name: CLIRunExportName, HostFunctions: []string{"aggrega_cli_write_stdout"}},
		},
	}
	if err := cli.Validate("acme/cli"); err != nil {
		t.Fatalf("cli type declaring a streaming host function: want accept, got %v", err)
	}
}

// TestHostFunctionAllowlistUnchanged pins the v1alpha1 host-function
// surface to exactly the curated set: the two baseline diagnostic
// functions, the external-input fetch channel, the two source callbacks
// a sourced provider guest uses to pack a resolved selection and to read
// a single file for content-based selection, and the producer-consumption
// callback a consumer guest uses to obtain a source's canonical tree
// through the host. A change that adds a host function without a
// corresponding decision fails here loudly rather than silently widening
// the bound surface.
func TestHostFunctionAllowlistUnchanged(t *testing.T) {
	t.Parallel()

	want := map[string]bool{
		"aggrega_log":                  true,
		"aggrega_emit_warning":         true,
		"aggrega_fetch_external_input": true,
		"aggrega_pack_source":          true,
		"aggrega_read_source_file":     true,
		"aggrega_parse_source":         true,
	}
	if !reflect.DeepEqual(hostFunctionAllowlist, want) {
		t.Fatalf("hostFunctionAllowlist changed: got %v, want %v", hostFunctionAllowlist, want)
	}
}
