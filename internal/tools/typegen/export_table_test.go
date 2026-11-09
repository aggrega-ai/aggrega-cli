// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
)

// TestExportTableHoldsEveryProviderHostExport reads the provider schema's export
// table through the registration that drives the plugin-wire registry and holds it
// to the provider type's host-export list in both directions, naming each export
// one side has and the other lacks rather than comparing counts. It also requires
// every definition a row names to be one the schema declares, read independently
// of the reader under test, so a row cannot pass by naming a definition that only
// the reader believes is there.
func TestExportTableHoldsEveryProviderHostExport(t *testing.T) {
	t.Parallel()

	var target codecTarget
	var base string
	for name, tgt := range codecTargets {
		if tgt.exportTable == plugintype.Provider {
			if base != "" {
				t.Fatalf("codec targets %q and %q both read the provider export table; one registration is the "+
					"table's one reader", base, name)
			}
			target, base = tgt, name
		}
	}
	if base == "" {
		t.Fatal("no codec target reads the provider schema's export table, so nothing holds it to the host-export list")
	}

	registrations, err := target.wireRegistrations()
	if err != nil {
		t.Fatalf("codec target %q: %v", base, err)
	}
	provider, ok := plugintype.Lookup(plugintype.Provider)
	if !ok || len(provider.HostExports) == 0 {
		t.Fatal("the provider type lists no host export, so there is nothing to hold the table to")
	}

	var inTable []string
	for _, r := range registrations {
		inTable = append(inTable, r.export)
	}
	for _, export := range provider.HostExports {
		if !slices.Contains(inTable, export) {
			t.Errorf("the provider host-export list holds %q, which the schema's export table does not map", export)
		}
	}
	for _, export := range inTable {
		if !slices.Contains(provider.HostExports, export) {
			t.Errorf("the schema's export table maps %q, which the provider host-export list does not hold", export)
		}
	}

	var doc struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal([]byte(target.schema), &doc); err != nil {
		t.Fatalf("the provider schema does not parse: %v", err)
	}
	for _, r := range registrations {
		for _, def := range []string{r.inputDef, r.outputDef} {
			if _, ok := doc.Defs[def]; !ok {
				t.Errorf("the row for %q names definition %q, which the schema's $defs does not declare", r.export, def)
			}
		}
	}
}

// exampleTableSchema builds a schema document carrying the given export table
// text and two declared definitions, ExampleInput and ExampleOutput.
func exampleTableSchema(table string) string {
	return `{"x-aggrega-exports": ` + table + `, "$defs": {"ExampleInput": {}, "ExampleOutput": {}}}`
}

// exampleRow is one table row mapping an export to the two declared definitions.
func exampleRow(export string) string {
	return fmt.Sprintf(`%q: {"input": "#/$defs/ExampleInput", "output": "#/$defs/ExampleOutput"}`, export)
}

// TestExportTableRefusesAMalformedTable drives each way a schema's export table
// can fail to say which definitions an export reads and answers with, beside a
// control that reads, and requires each to be refused naming what is wrong and the
// target it sits on. Each would otherwise emit a registry missing a row, carrying
// a row with no shape, or naming a type no codec declares.
func TestExportTableRefusesAMalformedTable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		schema  string
		wire    []wireExport
		wantSub string
	}{
		{name: "control: a well-formed row reads", schema: exampleTableSchema(`{` + exampleRow("only_export") + `}`)},
		{name: "no table", schema: `{"$defs": {"ExampleInput": {}}}`, wantSub: "declares no x-aggrega-exports table"},
		{name: "a table that is not an object", schema: exampleTableSchema(`[]`), wantSub: "is not an object"},
		{name: "an empty table", schema: exampleTableSchema(`{}`), wantSub: "holds no export"},
		{name: "an export named twice", schema: exampleTableSchema(`{` + exampleRow("twice") + `, ` + exampleRow("twice") + `}`), wantSub: `"twice" twice`},
		{name: "a row that is not an object", schema: exampleTableSchema(`{"only_export": "#/$defs/ExampleInput"}`), wantSub: "input and output alone"},
		{name: "a row with a member besides input and output", schema: exampleTableSchema(`{"only_export": {"input": "#/$defs/ExampleInput", "output": "#/$defs/ExampleOutput", "errors": "#/$defs/ExampleOutput"}}`), wantSub: "input and output alone"},
		{name: "a row without an output", schema: exampleTableSchema(`{"only_export": {"input": "#/$defs/ExampleInput"}}`), wantSub: "leaves out input or output"},
		{name: "a pointer outside $defs", schema: exampleTableSchema(`{"only_export": {"input": "#/properties/x", "output": "#/$defs/ExampleOutput"}}`), wantSub: "does not address one definition"},
		{name: "a pointer below a definition", schema: exampleTableSchema(`{"only_export": {"input": "#/$defs/ExampleInput/properties/x", "output": "#/$defs/ExampleOutput"}}`), wantSub: "does not address one definition"},
		{name: "a pointer naming an undeclared definition", schema: exampleTableSchema(`{"only_export": {"input": "#/$defs/ExampleInput", "output": "#/$defs/NoSuchOutput"}}`), wantSub: "does not declare"},
		{name: "a table read beside declared wireExports", schema: exampleTableSchema(`{` + exampleRow("only_export") + `}`), wire: []wireExport{{pluginType: plugintype.Provider, inputDef: "ExampleInput", outputDef: "ExampleOutput"}}, wantSub: "declared twice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			target := codecTarget{schema: tc.schema, schemaFile: "wasm-example-v1beta1.schema.json",
				pkgPath: "example.com/socket", exportTable: plugintype.Provider, wireExports: tc.wire}
			got, err := target.wireRegistrations()
			if tc.wantSub == "" {
				if err != nil {
					t.Fatalf("the control should read; got %v", err)
				}
				if len(got) != 1 || got[0].export != "only_export" || got[0].pluginType != plugintype.Provider ||
					got[0].inputDef != "ExampleInput" || got[0].outputDef != "ExampleOutput" {
					t.Fatalf("registrations = %+v; want one row for only_export reading ExampleInput and answering "+
						"with ExampleOutput", got)
				}
				return
			}
			if err == nil {
				t.Fatalf("the table read as %+v instead of being refused", got)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("the refusal does not name %q: %v", tc.wantSub, err)
			}
			// Through the registry walk the refusal also names the target it sits on.
			_, err = collectWireEntries(map[string]codecTarget{"example_gen.go": target})
			if err == nil || !strings.Contains(err.Error(), "example_gen.go") || !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("the registry walk did not refuse naming the target and %q: %v", tc.wantSub, err)
			}
		})
	}
}

// TestExportTableIsHeldToTheHostExportList drives the two ways a well-formed
// table can disagree with its plugin type's host-export list — an export the list
// holds and the table leaves out, and an export the table maps and the list does
// not hold — beside a control mapping exactly the list, and requires generation to
// refuse each naming the export. These are the refusals that hold the published
// table to the exports the host calls.
func TestExportTableIsHeldToTheHostExportList(t *testing.T) {
	t.Parallel()

	provider, ok := plugintype.Lookup(plugintype.Provider)
	if !ok || len(provider.HostExports) < 2 {
		t.Fatal("the provider type lists fewer than two host exports, so a table leaving one out still maps one")
	}
	table := func(exports []string) string {
		rows := make([]string, 0, len(exports))
		for _, export := range exports {
			rows = append(rows, exampleRow(export))
		}
		return exampleTableSchema("{" + strings.Join(rows, ", ") + "}")
	}
	const unlisted = "an_export_the_provider_type_does_not_list"
	last := provider.HostExports[len(provider.HostExports)-1]

	cases := []struct {
		name    string
		exports []string
		wantSub string
	}{
		{name: "control: the table maps exactly the list", exports: provider.HostExports},
		{name: "a listed export the table leaves out", exports: provider.HostExports[:len(provider.HostExports)-1], wantSub: last},
		{name: "a mapped export the list does not hold", exports: append(append([]string(nil), provider.HostExports...), unlisted), wantSub: unlisted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			target := codecTarget{schema: table(tc.exports), schemaFile: "wasm-example-v1beta1.schema.json",
				pkgPath: "example.com/socket", exportTable: plugintype.Provider}
			got, err := collectWireEntries(map[string]codecTarget{"example_gen.go": target})
			if tc.wantSub == "" {
				if err != nil {
					t.Fatalf("a table mapping exactly the list should generate; got %v", err)
				}
				if len(got) != len(provider.HostExports) {
					t.Fatalf("rows = %d; want one per listed export (%d)", len(got), len(provider.HostExports))
				}
				return
			}
			if err == nil {
				t.Fatalf("generation succeeded over a table that disagrees with the host-export list: %+v", got)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("the refusal does not name %q: %v", tc.wantSub, err)
			}
		})
	}
}
