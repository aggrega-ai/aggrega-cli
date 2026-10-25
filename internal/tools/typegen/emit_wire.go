// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
)

// generateWireRegistry emits the plugin-wire registry: the table binding each
// guest export a socket calls with a payload in the canonical binary encoding to
// the payload shape that export receives and the shape it answers with, a
// function that turns an authored text payload into those wire bytes, and one
// that supplies an empty answer of the export's shape for a guest's answer to be
// decoded into.
//
// It is derived rather than declared. There is no schema behind this target and
// no table beside it: the emitter walks the codec targets, reads each one's
// wireExports, and resolves an entry's role, or its plugin type and the listed
// export it names, to an export name through the plugin-type registry. So the
// fact the registry carries lives on the registrations that drive the codecs,
// and the export name itself is spelled in neither place — an entry reads it
// from the host-export lists the host's own dispatch identifiers are generated
// from, or names it by the constant those lists read and is checked against
// them. Every export those lists hold for a plugin type a registration serves
// gets its row, or generation fails.
//
// The emitted order is sorted by export name and then payload shape, because the
// target registry is a map: iteration order there is not stable, and a generated
// file has to be byte-identical from one run to the next.
func generateWireRegistry(t codecTarget) ([]byte, error) {
	entries, err := collectWireEntries(codecTargets)
	if err != nil {
		return nil, err
	}
	return emitWireRegistry(t, entries)
}

// emitWireRegistry writes the registry file for already-collected rows. It is
// separate from the collection so a test can hand it rows the real registrations
// do not carry.
//
// Several exports may serve one payload shape — two provider exports read the
// same render input, two verdict roles answer with one verdict envelope — so each
// shape's encoder or constructor is emitted once however many rows name it. Two
// shapes of one name declared in different packages would need two functions of
// one name, and the second declaration would not compile, so that is refused
// here instead, naming both packages.
func emitWireRegistry(t codecTarget, entries []wireEntry) ([]byte, error) {
	var body strings.Builder
	body.WriteString(wireRegistryTableDoc)
	body.WriteString("var entries = []entry{\n")
	for _, e := range entries {
		fmt.Fprintf(&body, "\t{export: %q, shape: %q, encode: %s, output: %q, newOutput: %s},\n",
			e.export, e.shape, e.encodeFunc(), e.output, e.newOutputFunc())
	}
	body.WriteString("}\n\n")

	emitted := map[string]string{}
	for _, e := range entries {
		if pkgPath, done := emitted[e.encodeFunc()]; done {
			if pkgPath != e.pkgPath {
				return nil, fmt.Errorf("payload shape %q is declared in both %s and %s, so its encoder "+
					"would be declared twice under one name", e.shape, pkgPath, e.pkgPath)
			}
			continue
		}
		emitted[e.encodeFunc()] = e.pkgPath
		fmt.Fprintf(&body, wireEncodeFuncTemplate, e.shape, e.encodeFunc(), selectorFor(e.pkgPath), e.shape)
	}
	for _, e := range entries {
		if pkgPath, done := emitted[e.newOutputFunc()]; done {
			if pkgPath != e.outputPkgPath {
				return nil, fmt.Errorf("output shape %q is declared in both %s and %s, so its constructor "+
					"would be declared twice under one name", e.output, pkgPath, e.outputPkgPath)
			}
			continue
		}
		emitted[e.newOutputFunc()] = e.outputPkgPath
		fmt.Fprintf(&body, wireNewOutputFuncTemplate, e.newOutputFunc(), e.newOutputFunc(), selectorFor(e.outputPkgPath), e.output)
	}

	imports := []string{"encoding/json"}
	if len(entries) > 0 {
		imports = append(imports, wasmPkgPath)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		for _, p := range []string{e.pkgPath, e.outputPkgPath} {
			if seen[p] {
				continue
			}
			seen[p] = true
			imports = append(imports, p)
		}
	}

	head := genHeaderImports("package "+t.pkg,
		"// its socket payload-codec registrations. DO NOT EDIT.\n",
		"// Regenerate with `go generate ./internal/tools/typegen` from the\n// aggrega-cli checkout.\n",
		imports)

	return formatSource(head, body.String())
}

// wireRegistryTableDoc is the godoc the emitted table carries. It is written
// here rather than in the destination package because the table is generated:
// the reason an entry exists belongs beside the entry a reader finds.
const wireRegistryTableDoc = `// entries binds each plugin export a socket calls with a payload in the
// canonical binary encoding to the payload shape that export receives and the
// shape it answers with.
//
// Which shapes an export receives and answers with is a fact nothing else can
// supply: it is not derivable from the export's name — a name is a symbol in the
// guest's export section and carries no shape with it. It is read from the
// payload-codec registrations, so the table and the codecs it names are generated
// from one source. An export absent from the table is one no socket calls with a
// payload in that encoding by default.
//
// Sorted by export name and then payload shape.
`

// wireEncodeFuncTemplate is the per-shape encoder: it reads the authored text
// payload into the shape and re-emits it in the canonical binary encoding.
//
// The read is encoding/json rather than a direct call to the shape's own
// unmarshaller, because one form has to serve both kinds of destination package:
// a package whose declarations the generator emits carries a generated
// unmarshaller, which encoding/json dispatches to, while a package whose
// declarations are hand-written carries none and is read by the standard
// library. So this introduces no second decode implementation — it selects
// whichever the shape already has.
const wireEncodeFuncTemplate = `// encode%s reads an authored text payload into that shape and
// re-emits it in the canonical binary encoding.
func %s(text []byte) ([]byte, error) {
	var v %s.%s
	if err := json.Unmarshal(text, &v); err != nil {
		return nil, err
	}
	return v.EncodeCBOR()
}

`

// wireNewOutputFuncTemplate is the per-output-shape constructor: it returns an
// empty value of the shape an export answers with, typed as what the host's
// socket-output decode entry point decodes into, so the caller decodes a guest's
// answer through that one entry point rather than through a reader of its own.
const wireNewOutputFuncTemplate = `// %s returns an empty value of that shape for a guest's answer
// to be decoded into.
func %s() wasm.GuestOutputCBOR { return new(%s.%s) }

`

// wasmPkgPath is the import path of the package declaring the host's
// socket-output decode entry point, whose destination type the emitted
// constructors return.
const wasmPkgPath = "github.com/aggrega-ai/aggrega-cli/internal/wasm"

// wireEntry is one emitted registry row: an export, the payload shape it
// receives and where that shape is declared, and the shape it answers with and
// where that one is declared.
type wireEntry struct {
	export        string
	shape         string
	pkgPath       string
	output        string
	outputPkgPath string
}

// selectorFor is the package selector the emitted body qualifies a shape
// declared at pkgPath with.
func selectorFor(pkgPath string) string {
	if alias, ok := importAliases[pkgPath]; ok {
		return alias
	}
	return pkgPath[strings.LastIndex(pkgPath, "/")+1:]
}

// encodeFunc is the name of the per-shape encoder emitted for this entry.
func (e wireEntry) encodeFunc() string { return "encode" + e.shape }

// newOutputFunc is the name of the per-output-shape constructor emitted for this
// entry.
func (e wireEntry) newOutputFunc() string { return "new" + e.output }

// collectWireEntries walks the given codec targets and returns one row per
// wireExports entry, in a sorted order so the emitted file is byte-identical run
// to run. It takes the targets as a parameter so each refusal can be driven over
// a registration built to trip it.
//
// It refuses rather than skipping in five cases, because each one would emit a
// registry that reads as complete and is not: a target carrying wireExports with
// no destination import path has no way to name its shape; an entry whose role
// or plugin type does not resolve to exactly one listed export — or that names an
// export its type's list does not hold — would contribute no export or a wrong
// one, and a socket whose plugin type lists nothing is refused here too; an entry
// whose shape the target does not emit a codec for would name a method that is
// not there; an entry whose output shape is missing, or is named in a package
// other than the one declaring it (see [wireExport.outputPackage]), would leave a
// caller printing a guest's answer with no type to decode it into, or the wrong
// one; and a plugin type a registration serves whose host-export list holds an
// export no entry declares would leave that export out of the table, so a caller
// converting an authored payload for it would hand the guest text it cannot
// read.
//
// The last refusal is what makes the host-export list and this table agree for
// every socket a registration serves: adding an export to such a type's list
// fails generation until a registration declares its payload shape. A type
// listing several exports reaches it too, because each registration on such a
// type must name the export it serves. A socket target that declares no export at
// all serves no type here, so nothing in this walk can see it; the plugin-wire
// package's call-site guard checks the same fact from the call sites' side, where
// such a socket's decode site names an export the table does not list.
//
// Targets are walked in sorted order so that, where more than one is wrong, the
// refusal reported is the same one on every run.
func collectWireEntries(targets map[string]codecTarget) ([]wireEntry, error) {
	return collectWireEntriesOver(targets, plugintype.Entries())
}

// collectWireEntriesOver is collectWireEntries over an explicit plugin-type
// registry, so a refusal the real registry does not trip — an empty host-export
// list, a role default missing from its list, a listed export no entry declares —
// can be driven over a registry built to trip it.
func collectWireEntriesOver(targets map[string]codecTarget, registry []plugintype.Entry) ([]wireEntry, error) {
	bases := make([]string, 0, len(targets))
	for base := range targets {
		bases = append(bases, base)
	}
	sort.Strings(bases)

	var out []wireEntry
	// servedBy records, per plugin type a registration serves, the targets
	// serving it, and declared the exports their entries declare, so the closing
	// check can name both sides of a gap.
	servedBy := map[plugintype.Type][]string{}
	declared := map[string]bool{}
	for _, base := range bases {
		t := targets[base]
		if len(t.wireExports) == 0 {
			continue
		}
		if t.pkgPath == "" {
			return nil, fmt.Errorf("typegen: codec target %q carries wireExports but no pkgPath, "+
				"so the registry cannot name the package its payload shapes are declared in", base)
		}
		for _, w := range t.wireExports {
			export, pluginType, err := w.exportName(registry)
			if err != nil {
				return nil, fmt.Errorf("codec target %q has a wireExports entry for %q that %w",
					base, w.inputDef, err)
			}
			if !t.emitsDef(w.inputDef) {
				return nil, fmt.Errorf("typegen: codec target %q names %q in wireExports, "+
					"which that target emits no codec for", base, w.inputDef)
			}
			outputPkgPath, err := w.outputPackage(t)
			if err != nil {
				return nil, fmt.Errorf("codec target %q has a wireExports entry for %q whose output %w",
					base, w.inputDef, err)
			}
			if !slices.Contains(servedBy[pluginType], base) {
				servedBy[pluginType] = append(servedBy[pluginType], base)
			}
			declared[export] = true
			out = append(out, wireEntry{
				export:        export,
				shape:         w.inputDef,
				pkgPath:       t.pkgPath,
				output:        w.outputDef,
				outputPkgPath: outputPkgPath,
			})
		}
	}

	// Every export a served plugin type lists has a row. The types are read in
	// registry order and their exports in list order, so the refusal reported is
	// the same one on every run.
	for _, entry := range registry {
		targetsServing, served := servedBy[entry.Type]
		if !served {
			continue
		}
		for _, export := range entry.HostExports {
			if !declared[export] {
				return nil, fmt.Errorf("the %s plugin type is served by codec target(s) %s, and its "+
					"host-export list holds %q, which no wireExports entry declares: every export a served plugin "+
					"type lists must have its payload shape declared, so that the plugin-wire registry converts an "+
					"authored payload for it into the encoding its guest reads",
					entry.Type, strings.Join(targetsServing, ", "), export)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].export != out[j].export {
			return out[i].export < out[j].export
		}
		return out[i].shape < out[j].shape
	})
	return out, nil
}

// outputPackage resolves the import path of the package declaring the entry's
// output shape on target t, refusing an entry that names no output shape at all
// and one whose package naming disagrees with what the target emits.
//
// A def the target emits is declared in the target's own package, so naming a
// package for it is a second answer that can only disagree with the first. A def
// the target skips is declared elsewhere — the host reads it through a published
// declaration — so the entry has to name that package, or the row would point at a
// type the target's own package does not hold.
func (w wireExport) outputPackage(t codecTarget) (string, error) {
	switch {
	case w.outputDef == "":
		return "", fmt.Errorf("shape is not named: every export the registry lists is answered in the " +
			"canonical binary encoding, so its entry names the shape the answer decodes into")
	case t.emitsDef(w.outputDef) && w.outputPkgPath != "":
		return "", fmt.Errorf("%q is emitted by this target, so it is declared in this target's own package "+
			"and naming %s for it as well is a second answer that can disagree", w.outputDef, w.outputPkgPath)
	case t.emitsDef(w.outputDef):
		return t.pkgPath, nil
	case w.outputPkgPath == "":
		return "", fmt.Errorf("%q is skipped by this target, so it is declared in another package, and the "+
			"entry names none", w.outputDef)
	default:
		return w.outputPkgPath, nil
	}
}
