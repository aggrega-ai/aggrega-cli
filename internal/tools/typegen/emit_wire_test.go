// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
)

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
// through the two canonical rules, and the expected name is read from those rules
// rather than spelled here, so this test holds no second copy of any export name.
func TestWireRegistryRefusesAnEntryItCannotResolve(t *testing.T) {
	t.Parallel()

	roleExport, ok := plugintype.GovernanceRoleExport(plugintype.RoleLabelTransform)
	if !ok {
		t.Fatal("the label-transform role has no default export, so the positive role row has nothing to compare")
	}
	importer, ok := plugintype.Lookup(plugintype.Importer)
	if !ok || importer.MinExport.Name == "" || importer.MinExport.IsPrefix {
		t.Fatal("the importer type has no exact required export, so the positive plugin-type row has nothing " +
			"to compare")
	}

	cases := []struct {
		name       string
		entry      wireExport
		wantExport string
		wantSub    string
	}{
		{name: "a role resolves to its default export", entry: wireExport{role: plugintype.RoleLabelTransform, inputDef: "X"}, wantExport: roleExport},
		{name: "a plugin type resolves to its required export", entry: wireExport{pluginType: plugintype.Importer, inputDef: "X"}, wantExport: importer.MinExport.Name},
		{name: "both keys", entry: wireExport{role: plugintype.RoleLabelTransform, pluginType: plugintype.Importer, inputDef: "X"}, wantSub: "both"},
		{name: "neither key", entry: wireExport{inputDef: "X"}, wantSub: "neither"},
		{name: "an unknown role", entry: wireExport{role: "no-such-role", inputDef: "X"}, wantSub: "no-such-role"},
		{name: "an unknown plugin type", entry: wireExport{pluginType: "no-such-type", inputDef: "X"}, wantSub: "no-such-type"},
		{name: "a plugin type whose rule is a prefix", entry: wireExport{pluginType: plugintype.Linter, inputDef: "X"}, wantSub: "no single export"},
		{name: "a plugin type with no rule", entry: wireExport{pluginType: plugintype.Governance, inputDef: "X"}, wantSub: "no single export"},
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
