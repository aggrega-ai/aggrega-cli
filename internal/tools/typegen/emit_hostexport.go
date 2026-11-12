// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
)

// generateHostExports emits the host-export identifiers: one package variable of
// the wasm package's host-export type per name a plugin type's host-export list
// holds, in the registry's own order.
//
// It is derived rather than declared. There is no schema behind this target and
// no list beside it: the emitter walks the plugin-type registry and writes, for
// each listed name, the identifier the compiler names that export by. So the
// name is spelled in the registry and nowhere a person edits besides, and an
// export the host is to call with a payload gets its identifier only by being
// added to its type's list.
//
// Each identifier is built at package start-up from the registry's list rather
// than from the name the emitter wrote, so a list that dropped a name this file
// still declares stops the program before any guest runs.
func generateHostExports(t codecTarget) ([]byte, error) {
	idents, err := collectHostExportIdents(plugintype.Entries())
	if err != nil {
		return nil, err
	}

	var body strings.Builder
	body.WriteString(hostExportsDoc)
	body.WriteString("var (\n")
	var current plugintype.Type
	for i, id := range idents {
		if id.pluginType != current {
			if i > 0 {
				body.WriteString("\n")
			}
			fmt.Fprintf(&body, "\t// The %s plugin type's host exports.\n\n", id.pluginType)
			current = id.pluginType
		}
		fmt.Fprintf(&body, "\t// %s is %s, a host export of the %s plugin type.\n", id.ident, id.export, id.pluginType)
		fmt.Fprintf(&body, "\t%s = listedExport(plugintype.%s, %q)\n", id.ident, id.typeIdent, id.export)
	}
	body.WriteString(")\n")

	head := genHeaderImports("package "+t.pkg,
		"// the plugin-type registry's host-export lists. DO NOT EDIT.\n",
		"// Regenerate with `go generate ./internal/tools/typegen` from the\n// aggrega-cli checkout.\n",
		[]string{"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"})
	return formatSource(head, body.String())
}

// hostExportsDoc opens the emitted variable block. It is written here rather
// than in the destination package because the block is generated: the reason the
// identifiers exist belongs beside them.
const hostExportsDoc = `// The exports the host calls a guest with a payload, one identifier per name the
// plugin-type registry's host-export lists hold, grouped by plugin type in the
// registry's order. They are the only values of HostExport there are, which is
// what makes the host's payload-carrying invocation refuse, at compile time, an
// export no list holds.
`

// hostExportIdent is one emitted identifier: the plugin type whose list holds
// the export, that type's identifier in the plugin-type package, the export's
// name, and the identifier the export is declared as.
type hostExportIdent struct {
	pluginType plugintype.Type
	typeIdent  string
	export     string
	ident      string
}

// collectHostExportIdents derives one identifier per listed host export, in
// registry order, and refuses a list the identifiers could not be emitted from.
// It takes the entries as a parameter so each refusal can be driven over a
// registry built to trip it.
//
// It refuses in two cases, because each would emit a file that reads as
// complete and is not: an empty name, which no guest exports and which would
// declare an identifier carrying nothing; and two listed names that derive the
// same identifier — the same name under two types, or two spellings Go
// identifiers cannot tell apart — since the second declaration would collide
// with the first and the compiler would have to name an export by something
// other than its name.
func collectHostExportIdents(entries []plugintype.Entry) ([]hostExportIdent, error) {
	var out []hostExportIdent
	seen := map[string]hostExportIdent{}
	for _, e := range entries {
		for _, name := range e.HostExports {
			if name == "" {
				return nil, fmt.Errorf("the %s plugin type's host-export list holds an empty name, "+
					"which names no export a guest can declare", e.Type)
			}
			id := hostExportIdent{
				pluginType: e.Type,
				typeIdent:  pluginTypeIdent(e.Type),
				export:     name,
				ident:      pascal(name) + "Export",
			}
			if prev, dup := seen[id.ident]; dup {
				return nil, fmt.Errorf("host export %q of the %s plugin type and host export %q of the %s "+
					"plugin type both derive the identifier %s, so the compiler could not name the two apart",
					prev.export, prev.pluginType, name, e.Type, id.ident)
			}
			seen[id.ident] = id
			out = append(out, id)
		}
	}
	return out, nil
}

// pluginTypeIdent is the identifier the plugin-type package declares a type's
// constant under: its canonical string with each hyphen read as a word break,
// in the same Go casing every generated identifier takes. A type whose constant
// does not follow that spelling makes the emitted file fail to compile, which
// names the mismatch at once.
func pluginTypeIdent(t plugintype.Type) string {
	return pascal(strings.ReplaceAll(string(t), "-", "_"))
}
