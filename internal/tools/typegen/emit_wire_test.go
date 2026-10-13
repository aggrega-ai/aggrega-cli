// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
)

// TestWireRegistryEmitsOneEncoderPerSharedShape hands the emitter two exports
// that read one payload shape, as two of the provider's exports do, and requires
// the shape's encoder to be declared exactly once — a second declaration of one
// name leaves the generated file uncompilable while generation itself succeeds —
// with a table row kept for each export. It then requires a shape name declared
// in two packages to be refused, since one encoder name cannot serve both.
func TestWireRegistryEmitsOneEncoderPerSharedShape(t *testing.T) {
	t.Parallel()

	target := codecTarget{pkg: "pluginwire"}
	shared := []wireEntry{
		{export: "first_export", shape: "SharedInput", binary: true, pkgPath: "example.com/socket"},
		{export: "second_export", shape: "SharedInput", binary: true, pkgPath: "example.com/socket"},
	}
	src, err := emitWireRegistry(target, shared)
	if err != nil {
		t.Fatalf("two exports sharing one payload shape should emit; got %v", err)
	}
	if got := strings.Count(string(src), "func encodeSharedInput("); got != 1 {
		t.Fatalf("the shared shape's encoder is declared %d times; want once:\n%s", got, src)
	}
	for _, e := range shared {
		if !strings.Contains(string(src), fmt.Sprintf("{export: %q, shape: %q", e.export, e.shape)) {
			t.Errorf("the table lost the row for %q:\n%s", e.export, src)
		}
	}

	clash := []wireEntry{
		{export: "first_export", shape: "SharedInput", binary: true, pkgPath: "example.com/socket"},
		{export: "second_export", shape: "SharedInput", binary: true, pkgPath: "example.com/other"},
	}
	_, err = emitWireRegistry(target, clash)
	if err == nil {
		t.Fatal("one shape name from two packages was emitted, so the generated file would declare its encoder twice")
	}
	for _, want := range []string{"SharedInput", "example.com/socket", "example.com/other"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}

// TestWireRegistryRefusesABinaryWireTargetDeclaringNoExport drives the refusal
// that makes declaring a binary-wire socket's exports mandatory. A target marked
// binaryWire with nothing in wireExports would generate a table that reads as
// complete while leaving that socket out of it, so the direct-invocation command
// would hand the socket's guest text it cannot read and nothing would report it.
//
// The control row is the same registration with its export declared, so the
// refusal is attributable to the missing declaration rather than to anything else
// about the target.
func TestWireRegistryRefusesABinaryWireTargetDeclaringNoExport(t *testing.T) {
	t.Parallel()

	base := codecTarget{
		schemaFile: "wasm-example-v1alpha1.schema.json",
		pkgPath:    "example.com/socket",
		binaryWire: true,
	}
	declared := base
	declared.wireExports = []wireExport{{pluginType: plugintype.Importer, inputDef: "ExampleInput"}}

	if _, err := collectWireEntries(map[string]codecTarget{"example_gen.go": declared}); err != nil {
		t.Fatalf("a binary-wire target declaring its export should generate; got %v", err)
	}

	_, err := collectWireEntries(map[string]codecTarget{"example_gen.go": base})
	if err == nil {
		t.Fatal("a binary-wire target declaring no export generated a registry; the refusal cannot fail, " +
			"so a socket can move to the binary wire with its exports missing from the table")
	}
	for _, want := range []string{"example_gen.go", "wasm-example-v1alpha1.schema.json", "binary wire", "wireExports"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}

// TestWireRegistryRefusalCanFailOverTheRealRegistrations takes every target the
// generator actually registers on the binary wire, strips its declared exports,
// and requires generation to refuse naming that target. A synthetic registration
// proves the refusal's logic; this proves it reaches the registrations that ship,
// so a target moved to the binary wire later is covered without this test being
// edited. It refuses an empty set of binary-wire targets, because a loop over
// nothing passes while proving nothing.
func TestWireRegistryRefusalCanFailOverTheRealRegistrations(t *testing.T) {
	t.Parallel()

	var binaryTargets []string
	for base, tgt := range codecTargets {
		if tgt.binaryWire {
			binaryTargets = append(binaryTargets, base)
		}
	}
	sort.Strings(binaryTargets)
	if len(binaryTargets) == 0 {
		t.Fatal("no codec target is registered on the binary wire, so there is nothing to strip and this " +
			"test proves nothing")
	}

	for _, stripped := range binaryTargets {
		t.Run(stripped, func(t *testing.T) {
			t.Parallel()
			weakened := make(map[string]codecTarget, len(codecTargets))
			for base, tgt := range codecTargets {
				weakened[base] = tgt
			}
			tgt := weakened[stripped]
			tgt.wireExports = nil
			weakened[stripped] = tgt

			_, err := collectWireEntries(weakened)
			if err == nil {
				t.Fatalf("generation succeeded with the exports of binary-wire target %q stripped", stripped)
			}
			if !strings.Contains(err.Error(), stripped) {
				t.Errorf("the refusal does not name the stripped target %q: %v", stripped, err)
			}
		})
	}
}

// TestWireRegistryRefusesAnEntryItCannotResolve covers each way a wireExports
// entry can fail to name exactly one export, every one of which would otherwise
// contribute a row naming no export or the wrong one. The positive rows resolve
// through the role rule and the host-export lists, and every export name an entry
// names or a row expects is read from those rather than spelled here, so this test
// holds no second copy of any export name.
func TestWireRegistryRefusesAnEntryItCannotResolve(t *testing.T) {
	t.Parallel()

	roleExport, ok := plugintype.GovernanceRoleExport(plugintype.RoleLabelTransform)
	if !ok {
		t.Fatal("the label-transform role has no default export, so the positive role row has nothing to compare")
	}
	importer, ok := plugintype.Lookup(plugintype.Importer)
	if !ok || len(importer.HostExports) != 1 {
		t.Fatal("the importer type lists no single host export, so the positive plugin-type row has nothing " +
			"to compare")
	}
	linter, ok := plugintype.Lookup(plugintype.Linter)
	if !ok || len(linter.HostExports) != 1 || !linter.MinExport.IsPrefix {
		t.Fatal("the linter type no longer lists one host export behind a prefix load rule, so the row " +
			"proving the list rather than the load rule decides has nothing to compare")
	}
	provider, ok := plugintype.Lookup(plugintype.Provider)
	if !ok || len(provider.HostExports) < 2 {
		t.Fatal("the provider type lists fewer than two host exports, so the row naming one of several has " +
			"nothing to choose from")
	}
	// The last listed export, so the row cannot pass by resolving to the first.
	oneOfSeveral := provider.HostExports[len(provider.HostExports)-1]

	cases := []struct {
		name       string
		entry      wireExport
		wantExport string
		wantSub    string
	}{
		{name: "a role resolves to its default export", entry: wireExport{role: plugintype.RoleLabelTransform, inputDef: "X"}, wantExport: roleExport},
		{name: "a plugin type resolves to its one host export", entry: wireExport{pluginType: plugintype.Importer, inputDef: "X"}, wantExport: importer.HostExports[0]},
		{name: "a plugin type whose load rule is a prefix resolves through its host-export list", entry: wireExport{pluginType: plugintype.Linter, inputDef: "X"}, wantExport: linter.HostExports[0]},
		{name: "a plugin type and its one listed export resolve to that export", entry: wireExport{pluginType: plugintype.Importer, export: importer.HostExports[0], inputDef: "X"}, wantExport: importer.HostExports[0]},
		{name: "a plugin type and one of its several listed exports resolve to that export", entry: wireExport{pluginType: plugintype.Provider, export: oneOfSeveral, inputDef: "X"}, wantExport: oneOfSeveral},
		{name: "both keys", entry: wireExport{role: plugintype.RoleLabelTransform, pluginType: plugintype.Importer, inputDef: "X"}, wantSub: "both"},
		{name: "a role and an export", entry: wireExport{role: plugintype.RoleLabelTransform, export: roleExport, inputDef: "X"}, wantSub: "both"},
		{name: "neither key", entry: wireExport{inputDef: "X"}, wantSub: "neither"},
		{name: "an export without its plugin type", entry: wireExport{export: importer.HostExports[0], inputDef: "X"}, wantSub: "without its plugin type"},
		{name: "an unknown role", entry: wireExport{role: "no-such-role", inputDef: "X"}, wantSub: "no-such-role"},
		{name: "an unknown plugin type", entry: wireExport{pluginType: "no-such-type", inputDef: "X"}, wantSub: "no-such-type"},
		{name: "a plugin type alone listing several host exports", entry: wireExport{pluginType: plugintype.Governance, inputDef: "X"}, wantSub: "no single export"},
		{name: "an export its plugin type's list does not hold", entry: wireExport{pluginType: plugintype.Provider, export: linter.HostExports[0], inputDef: "X"}, wantSub: linter.HostExports[0]},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			target := codecTarget{pkgPath: "example.com/socket", wireExports: []wireExport{tc.entry}}
			got, err := collectWireEntries(map[string]codecTarget{"example_gen.go": target})
			if tc.wantSub == "" {
				if err != nil {
					t.Fatalf("the entry should resolve; got %v", err)
				}
				if len(got) != 1 || got[0].export != tc.wantExport {
					t.Fatalf("rows = %+v; want one row for export %q", got, tc.wantExport)
				}
				return
			}
			if err == nil {
				t.Fatalf("the entry generated rows %+v instead of being refused", got)
			}
			if !strings.Contains(err.Error(), tc.wantSub) || !strings.Contains(err.Error(), "example_gen.go") {
				t.Errorf("the refusal does not name the target and %q: %v", tc.wantSub, err)
			}
		})
	}
}

// everyRole returns one wireExports entry per governance attachment role, each
// naming inputDef, so a built target serves the whole governance type as the real
// registrations together do.
func everyRole(inputDef string) []wireExport {
	var out []wireExport
	for _, role := range plugintype.GovernanceRoles() {
		out = append(out, wireExport{role: role, inputDef: inputDef})
	}
	return out
}

// withHostExports returns a copy of the real registry with plugin type t's
// host-export list replaced, so a refusal the shipped lists do not trip can be
// driven over a list built to trip it.
func withHostExports(t plugintype.Type, list []string) []plugintype.Entry {
	registry := plugintype.Entries()
	for i := range registry {
		if registry[i].Type == t {
			registry[i].HostExports = list
		}
	}
	return registry
}

// TestWireRegistryRefusesWhatTheHostExportListsLeaveUnsaid drives the refusals
// that tie the registry to the host-export lists, each over a list built to trip
// it beside a control over the real one: a binary-wire socket whose plugin type
// lists nothing, a role whose default its type's list does not hold, and a
// binary-wire plugin type listing an export no entry declares a payload shape
// for — both for a type listing one export and for one listing several, each of
// whose registrations names the export it serves. Each would emit a table that
// reads as complete while an export the host calls with a payload is missing from
// it.
func TestWireRegistryRefusesWhatTheHostExportListsLeaveUnsaid(t *testing.T) {
	t.Parallel()

	governance, ok := plugintype.Lookup(plugintype.Governance)
	if !ok || len(governance.HostExports) < 2 {
		t.Fatal("the governance type lists fewer than two host exports, so the role row has nothing to drop")
	}
	importer, ok := plugintype.Lookup(plugintype.Importer)
	if !ok || len(importer.HostExports) != 1 {
		t.Fatal("the importer type lists no single host export, so the completeness row has nothing to extend")
	}
	provider, ok := plugintype.Lookup(plugintype.Provider)
	if !ok || len(provider.HostExports) < 2 {
		t.Fatal("the provider type lists fewer than two host exports, so the several-export row proves nothing " +
			"a single-export row does not")
	}
	// One registration per export the provider lists, each naming its export. The
	// control proves every listed export can be registered by name; the built list
	// then adds one more.
	var everyProviderExport []wireExport
	for _, export := range provider.HostExports {
		everyProviderExport = append(everyProviderExport,
			wireExport{pluginType: plugintype.Provider, export: export, inputDef: "ExampleInput"})
	}
	renderVerdict, ok := plugintype.GovernanceRoleExport(plugintype.RoleRenderVerdict)
	if !ok {
		t.Fatal("the render-verdict role has no default export")
	}
	var withoutRenderVerdict []string
	for _, e := range governance.HostExports {
		if e != renderVerdict {
			withoutRenderVerdict = append(withoutRenderVerdict, e)
		}
	}
	const unregistered = "an_export_no_registration_declares"

	cases := []struct {
		name     string
		target   codecTarget
		registry []plugintype.Entry
		wantSubs []string
	}{
		{
			name: "a binary-wire socket whose plugin type lists no export",
			target: codecTarget{schemaFile: "wasm-example-v1alpha1.schema.json", pkgPath: "example.com/socket", binaryWire: true,
				wireExports: []wireExport{{pluginType: plugintype.Linter, inputDef: "ExampleInput"}}},
			registry: withHostExports(plugintype.Linter, nil),
			wantSubs: []string{"example_gen.go", string(plugintype.Linter), "host-export list is empty"},
		},
		{
			name: "a role whose default its type's list does not hold",
			target: codecTarget{schemaFile: "wasm-example-v1alpha1.schema.json", pkgPath: "example.com/socket", binaryWire: true,
				wireExports: everyRole("ExampleInput")},
			registry: withHostExports(plugintype.Governance, withoutRenderVerdict),
			wantSubs: []string{"example_gen.go", string(plugintype.RoleRenderVerdict), renderVerdict},
		},
		{
			name: "a binary-wire plugin type listing an export no entry declares",
			target: codecTarget{schemaFile: "wasm-example-v1alpha1.schema.json", pkgPath: "example.com/socket", binaryWire: true,
				wireExports: []wireExport{{pluginType: plugintype.Importer, export: importer.HostExports[0], inputDef: "ExampleInput"}}},
			registry: withHostExports(plugintype.Importer, append(append([]string(nil), importer.HostExports...), unregistered)),
			wantSubs: []string{"example_gen.go", string(plugintype.Importer), unregistered},
		},
		{
			name: "a binary-wire plugin type listing several exports, one of which no entry declares",
			target: codecTarget{schemaFile: "wasm-example-v1alpha1.schema.json", pkgPath: "example.com/socket", binaryWire: true,
				wireExports: everyProviderExport},
			registry: withHostExports(plugintype.Provider, append(append([]string(nil), provider.HostExports...), unregistered)),
			wantSubs: []string{"example_gen.go", string(plugintype.Provider), unregistered},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			targets := map[string]codecTarget{"example_gen.go": tc.target}
			if _, err := collectWireEntriesOver(targets, plugintype.Entries()); err != nil {
				t.Fatalf("the control over the real registry should generate, so the refusal below is the "+
					"built list's doing; got %v", err)
			}
			_, err := collectWireEntriesOver(targets, tc.registry)
			if err == nil {
				t.Fatal("generation succeeded over a host-export list built to be refused, so the refusal cannot fail")
			}
			for _, want := range tc.wantSubs {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
		})
	}
}

// TestWireRegistryRefusesAnUnregisteredExportOverTheRealRegistrations takes every
// plugin type the generator's real registrations serve on the binary wire, adds
// an export to that type's host-export list, and requires generation to refuse,
// naming the type and the export. It is the proof that adding an export to such a
// type's list cannot reach the host without its payload shape reaching the
// direct-invocation command's table too, and it covers a socket moved to the
// binary wire later without this test being edited. The refusal it reaches names
// the added export because every real registration names the export it serves, by
// role or by plugin type and export, so the added export is the one left without
// a registration rather than a registration left ambiguous. It refuses an empty
// set of binary-wire types, because a loop over nothing passes while proving
// nothing.
func TestWireRegistryRefusesAnUnregisteredExportOverTheRealRegistrations(t *testing.T) {
	t.Parallel()

	if _, err := collectWireEntries(codecTargets); err != nil {
		t.Fatalf("the real registrations should generate; got %v", err)
	}

	served := map[plugintype.Type]bool{}
	for _, tgt := range codecTargets {
		if !tgt.binaryWire {
			continue
		}
		for _, w := range tgt.wireExports {
			if _, pluginType, err := w.exportName(plugintype.Entries()); err == nil {
				served[pluginType] = true
			}
		}
	}
	var types []string
	for pt := range served {
		types = append(types, string(pt))
	}
	sort.Strings(types)
	if len(types) == 0 {
		t.Fatal("no plugin type is served on the binary wire, so there is nothing to extend and this test proves nothing")
	}

	const added = "an_export_added_without_a_registration"
	for _, name := range types {
		pt := plugintype.Type(name)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			entry, _ := plugintype.Lookup(pt)
			registry := withHostExports(pt, append(append([]string(nil), entry.HostExports...), added))
			_, err := collectWireEntriesOver(codecTargets, registry)
			if err == nil {
				t.Fatalf("generation succeeded with %q added to the %s type's host-export list and no registration "+
					"declaring it", added, pt)
			}
			for _, want := range []string{string(pt), added} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
		})
	}
}
