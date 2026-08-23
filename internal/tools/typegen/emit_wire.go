// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
)

// generateWireRegistry emits the plugin-wire registry: the table binding each
// guest export to the payload shape that export receives, the encoding its wire
// carries, and a function that turns an authored text payload into those wire
// bytes.
//
// It is derived rather than declared. There is no schema behind this target and
// no table beside it: the emitter walks the codec targets, reads each one's
// wireExports and binaryWire, and resolves an entry's role to an export name
// through the canonical role-to-export rule. So the two facts the registry
// carries live on the registrations that drive the codecs, and the export name
// itself is spelled in neither place — it is read from the same rule the host's
// own dispatch default reads.
//
// The emitted order is sorted by export name and then payload shape, because the
// target registry is a map: iteration order there is not stable, and a generated
// file has to be byte-identical from one run to the next.
func generateWireRegistry(t codecTarget) ([]byte, error) {
	entries, err := collectWireEntries()
	if err != nil {
		return nil, err
	}

	var body strings.Builder
	body.WriteString(wireRegistryTableDoc)
	body.WriteString("var entries = []entry{\n")
	for _, e := range entries {
		fmt.Fprintf(&body, "\t{export: %q, shape: %q, binary: %t, encode: %s},\n",
			e.export, e.shape, e.binary, e.encodeFunc())
	}
	body.WriteString("}\n\n")

	for _, e := range entries {
		fmt.Fprintf(&body, wireEncodeFuncTemplate, e.shape, e.encodeFunc(), e.selector(), e.shape)
	}

	imports := []string{"encoding/json"}
	seen := map[string]bool{}
	for _, e := range entries {
		if seen[e.pkgPath] {
			continue
		}
		seen[e.pkgPath] = true
		imports = append(imports, e.pkgPath)
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
const wireRegistryTableDoc = `// entries binds each plugin export to the payload shape it receives and to the
// encoding that export's wire carries.
//
// Two facts sit here that nothing else can supply. Which shape an export
// receives is not derivable from the export's name — one socket's schema serves
// two pipeline stages with two different input shapes under one export name —
// and whether that export's wire is binary is fixed by the socket rather than by
// anything a caller can see. Both are read from the payload-codec registrations,
// so the table and the codecs it names are generated from one source.
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
//
// It is emitted for every entry, binary or not, so that changing an export's
// wire is a one-field edit on the registration rather than new code here.
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

// wireEntry is one emitted registry row: an export, the payload shape it
// receives, the encoding its wire carries, and where that shape is declared.
type wireEntry struct {
	export  string
	shape   string
	binary  bool
	pkgPath string
}

// selector is the package selector the emitted body qualifies the shape with.
func (e wireEntry) selector() string {
	if alias, ok := importAliases[e.pkgPath]; ok {
		return alias
	}
	return e.pkgPath[strings.LastIndex(e.pkgPath, "/")+1:]
}

// encodeFunc is the name of the per-shape encoder emitted for this entry.
func (e wireEntry) encodeFunc() string { return "encode" + e.shape }

// collectWireEntries walks the codec targets and returns one row per wireExports
// entry, in a sorted order so the emitted file is byte-identical run to run.
//
// It refuses rather than skipping in three cases, because each one would emit a
// registry that reads as complete and is not: a target carrying wireExports with
// no destination import path has no way to name its shape; an entry naming a
// role the canonical role-to-export rule does not know would silently contribute
// no export; and an entry whose shape the target does not emit a codec for would
// name a method that is not there.
func collectWireEntries() ([]wireEntry, error) {
	var out []wireEntry
	for base, t := range codecTargets {
		if len(t.wireExports) == 0 {
			continue
		}
		if t.pkgPath == "" {
			return nil, fmt.Errorf("typegen: codec target %q carries wireExports but no pkgPath, "+
				"so the registry cannot name the package its payload shapes are declared in", base)
		}
		for _, w := range t.wireExports {
			export, ok := plugintype.GovernanceRoleExport(w.role)
			if !ok {
				return nil, fmt.Errorf("typegen: codec target %q names role %q in wireExports, "+
					"which the canonical role-to-export rule does not know", base, string(w.role))
			}
			if !t.emitsDef(w.inputDef) {
				return nil, fmt.Errorf("typegen: codec target %q names %q in wireExports, "+
					"which that target emits no codec for", base, w.inputDef)
			}
			out = append(out, wireEntry{
				export:  export,
				shape:   w.inputDef,
				binary:  t.binaryWire,
				pkgPath: t.pkgPath,
			})
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
