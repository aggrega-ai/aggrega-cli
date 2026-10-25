// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
)

// TestWireRegistryEmitsOneEncoderPerSharedShape hands the emitter two exports
// that read one payload shape and answer with one output shape, as two of the
// provider's exports read one input and the two verdict roles answer with one
// envelope, and requires each shape's function to be declared exactly once — a
// second declaration of one name leaves the generated file uncompilable while
// generation itself succeeds — with a table row kept for each export. It then
// requires a shape name declared in two packages to be refused, on either side,
// since one function name cannot serve both.
func TestWireRegistryEmitsOneEncoderPerSharedShape(t *testing.T) {
	t.Parallel()

	target := codecTarget{pkg: "pluginwire"}
	shared := []wireEntry{
		{export: "first_export", shape: "SharedInput", pkgPath: "example.com/socket", output: "SharedOutput", outputPkgPath: "example.com/published"},
		{export: "second_export", shape: "SharedInput", pkgPath: "example.com/socket", output: "SharedOutput", outputPkgPath: "example.com/published"},
	}
	src, err := emitWireRegistry(target, shared)
	if err != nil {
		t.Fatalf("two exports sharing one payload shape should emit; got %v", err)
	}
	if got := strings.Count(string(src), "func encodeSharedInput("); got != 1 {
		t.Fatalf("the shared shape's encoder is declared %d times; want once:\n%s", got, src)
	}
	if got := strings.Count(string(src), "func newSharedOutput("); got != 1 {
		t.Fatalf("the shared output shape's constructor is declared %d times; want once:\n%s", got, src)
	}
	if !strings.Contains(string(src), "return new(published.SharedOutput)") {
		t.Errorf("the constructor does not build the output shape from the package declaring it:\n%s", src)
	}
	for _, e := range shared {
		row := fmt.Sprintf("{export: %q, shape: %q, encode: encodeSharedInput, output: %q, newOutput: newSharedOutput}",
			e.export, e.shape, e.output)
		if !strings.Contains(string(src), row) {
			t.Errorf("the table lost the row for %q:\n%s", e.export, src)
		}
	}

	clashes := []struct {
		name    string
		entries []wireEntry
		want    []string
	}{
		{
			name: "an input shape",
			entries: []wireEntry{
				{export: "first_export", shape: "SharedInput", pkgPath: "example.com/socket", output: "FirstOutput", outputPkgPath: "example.com/socket"},
				{export: "second_export", shape: "SharedInput", pkgPath: "example.com/other", output: "SecondOutput", outputPkgPath: "example.com/other"},
			},
			want: []string{"SharedInput", "example.com/socket", "example.com/other"},
		},
		{
			name: "an output shape",
			entries: []wireEntry{
				{export: "first_export", shape: "FirstInput", pkgPath: "example.com/socket", output: "SharedOutput", outputPkgPath: "example.com/socket"},
				{export: "second_export", shape: "SecondInput", pkgPath: "example.com/socket", output: "SharedOutput", outputPkgPath: "example.com/other"},
			},
			want: []string{"SharedOutput", "example.com/socket", "example.com/other"},
		},
	}
	for _, tc := range clashes {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := emitWireRegistry(target, tc.entries)
			if err == nil {
				t.Fatal("one shape name from two packages was emitted, so the generated file would declare its function twice")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
		})
	}
}

// TestWireRegistryRefusesAnOutputItCannotPlace covers each way an entry's
// output shape can fail to name the type the host decodes an answer into, beside
// the two ways it names one: a def the target emits, read from the target's own
// package, and a def the target skips, read from the package the entry names.
func TestWireRegistryRefusesAnOutputItCannotPlace(t *testing.T) {
	t.Parallel()

	importer, ok := plugintype.Lookup(plugintype.Importer)
	if !ok || len(importer.HostExports) != 1 {
		t.Fatal("the importer type lists no single host export, so the rows have no export to register")
	}
	entry := func(outputDef, outputPkgPath string) wireExport {
		return wireExport{pluginType: plugintype.Importer, export: importer.HostExports[0], inputDef: "ExampleInput",
			outputDef: outputDef, outputPkgPath: outputPkgPath}
	}

	cases := []struct {
		name        string
		entry       wireExport
		skipDefs    []string
		wantPkgPath string
		wantSub     string
	}{
		{name: "an output the target emits is read from the target's package", entry: entry("ExampleOutput", ""), wantPkgPath: "example.com/socket"},
		{name: "an output the target skips is read from the named package", entry: entry("ExampleOutput", "example.com/published"), skipDefs: []string{"ExampleOutput"}, wantPkgPath: "example.com/published"},
		{name: "no output shape", entry: entry("", ""), wantSub: "is not named"},
		{name: "a package named for an output the target emits", entry: entry("ExampleOutput", "example.com/published"), wantSub: "second answer"},
		{name: "no package named for an output the target skips", entry: entry("ExampleOutput", ""), skipDefs: []string{"ExampleOutput"}, wantSub: "names none"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			target := codecTarget{pkgPath: "example.com/socket", skipDefs: tc.skipDefs, wireExports: []wireExport{tc.entry}}
			got, err := collectWireEntries(map[string]codecTarget{"example_gen.go": target})
			if tc.wantSub == "" {
				if err != nil {
					t.Fatalf("the output should resolve; got %v", err)
				}
				if len(got) != 1 || got[0].output != tc.entry.outputDef || got[0].outputPkgPath != tc.wantPkgPath {
					t.Fatalf("rows = %+v; want one row answering with %q from %q", got, tc.entry.outputDef, tc.wantPkgPath)
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

// TestWireRegistryRefusesAnEntryItCannotResolve covers each way a wireExports
// entry can fail to name exactly one export, every one of which would otherwise
// contribute a row naming no export or the wrong one. The positive rows resolve
// through the role rule and the host-export lists, and every export name an entry
// names or a row expects is read from those rather than spelled here, so this test
// holds no second copy of any export name.
//
// A positive row whose plugin type lists several exports carries the rest of
// that list beside its entry, under a shape of their own. Generation refuses a
// served plugin type that leaves a listed export undeclared, and that refusal is
// TestWireRegistryRefusesWhatTheHostExportListsLeaveUnsaid's subject, not this
// one's; so a positive row serves its type completely and is read by its own
// shape alone. The row still fails if its entry resolves wrongly: the export the
// row expects is then the one left undeclared.
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
		name  string
		entry wireExport
		// rest declares the other exports the entry's plugin type lists, for a
		// positive row on a type listing several.
		rest       []wireExport
		wantExport string
		wantSub    string
	}{
		{name: "a role resolves to its default export", entry: wireExport{role: plugintype.RoleLabelTransform, inputDef: "X", outputDef: "XOutput"}, rest: declaringTheRest(t, plugintype.Governance, roleExport), wantExport: roleExport},
		{name: "a plugin type resolves to its one host export", entry: wireExport{pluginType: plugintype.Importer, inputDef: "X", outputDef: "XOutput"}, wantExport: importer.HostExports[0]},
		{name: "a plugin type whose load rule is a prefix resolves through its host-export list", entry: wireExport{pluginType: plugintype.Linter, inputDef: "X", outputDef: "XOutput"}, wantExport: linter.HostExports[0]},
		{name: "a plugin type and its one listed export resolve to that export", entry: wireExport{pluginType: plugintype.Importer, export: importer.HostExports[0], inputDef: "X", outputDef: "XOutput"}, wantExport: importer.HostExports[0]},
		{name: "a plugin type and one of its several listed exports resolve to that export", entry: wireExport{pluginType: plugintype.Provider, export: oneOfSeveral, inputDef: "X", outputDef: "XOutput"}, rest: declaringTheRest(t, plugintype.Provider, oneOfSeveral), wantExport: oneOfSeveral},
		{name: "both keys", entry: wireExport{role: plugintype.RoleLabelTransform, pluginType: plugintype.Importer, inputDef: "X", outputDef: "XOutput"}, wantSub: "both"},
		{name: "a role and an export", entry: wireExport{role: plugintype.RoleLabelTransform, export: roleExport, inputDef: "X", outputDef: "XOutput"}, wantSub: "both"},
		{name: "neither key", entry: wireExport{inputDef: "X", outputDef: "XOutput"}, wantSub: "neither"},
		{name: "an export without its plugin type", entry: wireExport{export: importer.HostExports[0], inputDef: "X", outputDef: "XOutput"}, wantSub: "without its plugin type"},
		{name: "an unknown role", entry: wireExport{role: "no-such-role", inputDef: "X", outputDef: "XOutput"}, wantSub: "no-such-role"},
		{name: "an unknown plugin type", entry: wireExport{pluginType: "no-such-type", inputDef: "X", outputDef: "XOutput"}, wantSub: "no-such-type"},
		{name: "a plugin type alone listing several host exports", entry: wireExport{pluginType: plugintype.Governance, inputDef: "X", outputDef: "XOutput"}, wantSub: "no single export"},
		{name: "an export its plugin type's list does not hold", entry: wireExport{pluginType: plugintype.Provider, export: linter.HostExports[0], inputDef: "X", outputDef: "XOutput"}, wantSub: linter.HostExports[0]},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			target := codecTarget{pkgPath: "example.com/socket",
				wireExports: append([]wireExport{tc.entry}, tc.rest...)}
			got, err := collectWireEntries(map[string]codecTarget{"example_gen.go": target})
			if tc.wantSub == "" {
				if err != nil {
					t.Fatalf("the entry should resolve; got %v", err)
				}
				var own []wireEntry
				for _, row := range got {
					if row.shape == tc.entry.inputDef {
						own = append(own, row)
					}
				}
				if len(own) != 1 || own[0].export != tc.wantExport {
					t.Fatalf("rows for the entry's shape = %+v; want one row for export %q", own, tc.wantExport)
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

// declaringTheRest returns one wireExports entry for every export plugin type pt
// lists other than except, each naming the shape "Rest", so a target holding
// them beside an entry for except serves pt completely while that entry stays the
// only one of its own shape.
func declaringTheRest(t *testing.T, pt plugintype.Type, except string) []wireExport {
	t.Helper()
	entry, ok := plugintype.Lookup(pt)
	if !ok || !slices.Contains(entry.HostExports, except) {
		t.Fatalf("the %s type does not list %q, so there is no rest of its list to declare beside it", pt, except)
	}
	var out []wireExport
	for _, export := range entry.HostExports {
		if export != except {
			out = append(out, wireExport{pluginType: pt, export: export, inputDef: "Rest", outputDef: "RestOutput"})
		}
	}
	return out
}

// everyRole returns one wireExports entry per governance attachment role, each
// naming inputDef, so a built target serves the whole governance type as the real
// registrations together do.
func everyRole(inputDef string) []wireExport {
	var out []wireExport
	for _, role := range plugintype.GovernanceRoles() {
		out = append(out, wireExport{role: role, inputDef: inputDef, outputDef: inputDef + "Output"})
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
// it beside a control over the real one: a socket whose plugin type lists
// nothing, a role whose default its type's list does not hold, and a served
// plugin type listing an export no entry declares a payload shape for — both for
// a type listing one export and for one listing several, each of whose
// registrations names the export it serves. Each would emit a table that reads as
// complete while an export the host calls with a payload is missing from it.
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
			wireExport{pluginType: plugintype.Provider, export: export, inputDef: "ExampleInput", outputDef: "ExampleOutput"})
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
			name: "a socket whose plugin type lists no export",
			target: codecTarget{schemaFile: "wasm-example-v1alpha1.schema.json", pkgPath: "example.com/socket",
				wireExports: []wireExport{{pluginType: plugintype.Linter, inputDef: "ExampleInput", outputDef: "ExampleOutput"}}},
			registry: withHostExports(plugintype.Linter, nil),
			wantSubs: []string{"example_gen.go", string(plugintype.Linter), "host-export list is empty"},
		},
		{
			name: "a role whose default its type's list does not hold",
			target: codecTarget{schemaFile: "wasm-example-v1alpha1.schema.json", pkgPath: "example.com/socket",
				wireExports: everyRole("ExampleInput")},
			registry: withHostExports(plugintype.Governance, withoutRenderVerdict),
			wantSubs: []string{"example_gen.go", string(plugintype.RoleRenderVerdict), renderVerdict},
		},
		{
			name: "a served plugin type listing an export no entry declares",
			target: codecTarget{schemaFile: "wasm-example-v1alpha1.schema.json", pkgPath: "example.com/socket",
				wireExports: []wireExport{{pluginType: plugintype.Importer, export: importer.HostExports[0], inputDef: "ExampleInput", outputDef: "ExampleOutput"}}},
			registry: withHostExports(plugintype.Importer, append(append([]string(nil), importer.HostExports...), unregistered)),
			wantSubs: []string{"example_gen.go", string(plugintype.Importer), unregistered},
		},
		{
			name: "a served plugin type listing several exports, one of which no entry declares",
			target: codecTarget{schemaFile: "wasm-example-v1alpha1.schema.json", pkgPath: "example.com/socket",
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
// plugin type the generator's real registrations serve, adds an export to that
// type's host-export list, and requires generation to refuse, naming the type and
// the export. It is the proof that adding an export to such a type's list cannot
// reach the host without its payload shape reaching the direct-invocation
// command's table too, and it covers a socket registered later without this test
// being edited. The refusal it reaches names the added export because every real
// registration names the export it serves, by role or by plugin type and export,
// so the added export is the one left without a registration rather than a
// registration left ambiguous. It refuses an empty set of served types, because a
// loop over nothing passes while proving nothing.
func TestWireRegistryRefusesAnUnregisteredExportOverTheRealRegistrations(t *testing.T) {
	t.Parallel()

	if _, err := collectWireEntries(codecTargets); err != nil {
		t.Fatalf("the real registrations should generate; got %v", err)
	}

	served := map[plugintype.Type]bool{}
	for _, tgt := range codecTargets {
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
		t.Fatal("no plugin type is served by a registration, so there is nothing to extend and this test proves nothing")
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
