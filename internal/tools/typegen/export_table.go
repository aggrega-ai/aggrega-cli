// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// exportTableKeyword is the top-level keyword under which a socket schema
// publishes, for each export the host calls a guest with, the definition of that
// export's input and the definition of its output.
//
// The table is what a reader of the schema resolves a payload's shape by, so it
// is the one home of that binding: a codec target naming exportTable reads its
// wire registrations from it rather than declaring them a second time beside it.
const exportTableKeyword = "x-aggrega-exports"

// defPointerPrefix is the start of every pointer the export table holds. A
// definition is addressed as a JSON Pointer fragment into the same document's
// $defs, which is the form a consumer appends to the schema's $id to compile the
// definition with any JSON Schema validator.
const defPointerPrefix = "#/$defs/"

// exportTableRow is one export of a schema's export table: the export's name and
// the $defs names of its input and its output.
type exportTableRow struct {
	export string
	input  string
	output string
}

// wireRegistrations returns the wire registrations a codec target declares — its
// wireExports, or, for a target naming exportTable, one registration per row of
// its schema's export table, each naming that plugin type and the row's export.
//
// A registration read from the table passes through exactly the checks a
// declared one does: the export has to be one the plugin type's host-export list
// holds, and every export that list holds has to have a row. So the table is held
// to the host's export list in both directions by generation itself.
//
// A target naming both is refused, because its exports would then be declared
// twice and the two could disagree.
func (t codecTarget) wireRegistrations() ([]wireExport, error) {
	if t.exportTable == "" {
		return t.wireExports, nil
	}
	if len(t.wireExports) > 0 {
		return nil, fmt.Errorf("declares wireExports and also reads them from its schema's %s table, so its "+
			"exports are declared twice", exportTableKeyword)
	}
	rows, err := readExportTable(t.schema)
	if err != nil {
		return nil, fmt.Errorf("reads its wire registrations from %s: %w", t.schemaFile, err)
	}
	out := make([]wireExport, 0, len(rows))
	for _, r := range rows {
		out = append(out, wireExport{pluginType: t.exportTable, export: r.export, inputDef: r.input, outputDef: r.output})
	}
	return out, nil
}

// readExportTable reads a schema document's export table, in the order the
// document writes it.
//
// It refuses rather than skipping, because a table read past a defect would emit
// a registry that reads as complete and is not: a document carrying no table, or
// one that is not an object or holds no export; an export named twice, which a
// decoder into a map would collapse silently onto its last value; a row that is
// not an object, carries a member other than input and output, or leaves either
// out; and a pointer that does not address a definition this document declares.
func readExportTable(schema string) ([]exportTableRow, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(schema), &root); err != nil {
		return nil, fmt.Errorf("the schema does not parse as a JSON object: %w", err)
	}
	raw, ok := root[exportTableKeyword]
	if !ok {
		return nil, fmt.Errorf("the schema declares no %s table", exportTableKeyword)
	}
	var defs map[string]json.RawMessage
	if rawDefs, ok := root["$defs"]; ok {
		if err := json.Unmarshal(rawDefs, &defs); err != nil {
			return nil, fmt.Errorf("the schema's $defs is not an object: %w", err)
		}
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("the %s table is not an object", exportTableKeyword)
	}
	var rows []exportTableRow
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("the %s table does not parse: %w", exportTableKeyword, err)
		}
		export, _ := tok.(string)
		if export == "" {
			return nil, fmt.Errorf("the %s table names an empty export", exportTableKeyword)
		}
		if seen[export] {
			return nil, fmt.Errorf("the %s table names export %q twice", exportTableKeyword, export)
		}
		seen[export] = true

		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, fmt.Errorf("the %s table's row for %q does not parse: %w", exportTableKeyword, export, err)
		}
		row, err := readExportTableRow(export, value, defs)
		if err != nil {
			return nil, fmt.Errorf("the %s table's row for %q %w", exportTableKeyword, export, err)
		}
		rows = append(rows, row)
	}
	if _, err := dec.Token(); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("the %s table does not parse: %w", exportTableKeyword, err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("the %s table holds no export", exportTableKeyword)
	}
	return rows, nil
}

// readExportTableRow reads one row's two pointers and resolves each to the name
// of a definition the document declares.
func readExportTableRow(export string, value json.RawMessage, defs map[string]json.RawMessage) (exportTableRow, error) {
	var members struct {
		Input  *string `json:"input"`
		Output *string `json:"output"`
	}
	dec := json.NewDecoder(bytes.NewReader(value))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&members); err != nil {
		return exportTableRow{}, fmt.Errorf("is not an object holding input and output alone: %w", err)
	}
	if members.Input == nil || members.Output == nil {
		return exportTableRow{}, errors.New("leaves out input or output: every export the table names has both")
	}
	input, err := defNameAt(*members.Input, defs)
	if err != nil {
		return exportTableRow{}, fmt.Errorf("has an input that %w", err)
	}
	output, err := defNameAt(*members.Output, defs)
	if err != nil {
		return exportTableRow{}, fmt.Errorf("has an output that %w", err)
	}
	return exportTableRow{export: export, input: input, output: output}, nil
}

// defNameAt returns the definition name a pointer addresses, refusing a pointer
// that addresses anything other than one definition of this document's $defs.
func defNameAt(pointer string, defs map[string]json.RawMessage) (string, error) {
	name, ok := strings.CutPrefix(pointer, defPointerPrefix)
	if !ok || name == "" || strings.Contains(name, "/") {
		return "", fmt.Errorf("is %q, which does not address one definition as %s<name>", pointer, defPointerPrefix)
	}
	if _, declared := defs[name]; !declared {
		return "", fmt.Errorf("is %q, which names a definition the schema does not declare", pointer)
	}
	return name, nil
}
