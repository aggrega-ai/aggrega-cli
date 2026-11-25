// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"slices"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// Import turns one import request into the Aggregate, the Release and the
// catalog entities a source in the format f drafts, with a decision on each of
// its resources and what each step reports, laid out as the importer kit lays
// out every importer's drafts.
//
// It reads the root setting, reads the request's sources into a graph through
// f.Read, writes each repeated node as the nodes it deploys ([Expand]),
// classes every node and drafts the Component and the Recipe of each node no
// Component maps ([Classify]), then writes the Aggregate ([Aggregate]) and the
// Release, with the Manifest naming the realizations it names ([Release]).
//
// The decisions are those of the classified nodes, in the order Classify
// returns them. The entities are the reference of each mapped node and the
// Component and the Recipe drafted for each Component name, once, in that
// order, followed by the Aggregate, the Release and the Manifest. The files
// are the Aggregate, the Release and the Manifest as importer.Render lays them
// out, followed by the drafted Components and Recipes as importer.CatalogFiles
// lays them out under catalog/; under the render_mode setting llm, where each
// writes the decisions alone as one document, that document is written once.
//
// Any halt, raised anywhere, drafts nothing: the output carries the
// diagnostics alone. A format with no reader halts source-refused. Where the
// graph read holds no node, or holds none once its repetitions are expanded,
// the import warns no-nodes for each source and drafts nothing. Every list is
// built in the order of the graph's nodes, so two runs over one request give
// one byte sequence.
func Import(in aggrega.ImportInput, f Format, parse ParseFunc) aggrega.ImportOutput {
	settings, halt := ReadSettings(in.Settings)
	if halt != nil {
		return diagnosticsAlone(append([]aggrega.Diagnostic(nil), *halt))
	}
	if f.Read == nil {
		return diagnosticsAlone(append([]aggrega.Diagnostic(nil), Report(CodeSourceRefused, "the importer's format "+
			"has no reader, so no source is read: the format is the importer's own, so report it to the importer's "+
			"authors")))
	}
	g, diags := f.Read(in, settings, parse)
	if halts(diags) {
		return diagnosticsAlone(diags)
	}
	if !holdsNode(g) {
		return diagnosticsAlone(append(diags, noNodes(in, f)...))
	}
	diags = append(diags, Expand(g)...)
	if !holdsNode(g) {
		return diagnosticsAlone(append(diags, noNodes(in, f)...))
	}
	classified, more := Classify(in, f, g)
	diags = append(diags, more...)
	if halts(diags) {
		return diagnosticsAlone(diags)
	}
	aggregate, more := Aggregate(in, f, g, classified)
	diags = append(diags, more...)
	documents, more := Release(in, f, g, classified, aggregate)
	diags = append(diags, more...)
	if halts(diags) {
		return diagnosticsAlone(diags)
	}
	documents = append([]aggrega.EmittedEntity{aggregate}, documents...)

	out := aggrega.ImportOutput{Diagnostics: diags}
	var drafts []importer.CatalogDraft
	for _, cl := range classified {
		out.Decisions = append(out.Decisions, cl.Decision)
		switch {
		case cl.Class == ClassMapped:
			out.Entities = append(out.Entities, cl.Entity)
		case len(cl.Drafts) > 0:
			drafts = append(drafts, cl.Drafts...)
			for _, d := range cl.Drafts {
				out.Entities = append(out.Entities, d.Entity)
			}
		}
	}
	out.Entities = append(out.Entities, documents...)
	out.Files = importer.Render(in.Settings, documents, out.Decisions)
	for _, file := range importer.CatalogFiles(in.Settings, drafts, out.Decisions) {
		if !slices.ContainsFunc(out.Files, func(w aggrega.ScaffoldFile) bool { return w.Path == file.Path }) {
			out.Files = append(out.Files, file)
		}
	}
	return out
}

// diagnosticsAlone is the output of an import that drafts nothing.
func diagnosticsAlone(diags []aggrega.Diagnostic) aggrega.ImportOutput {
	return aggrega.ImportOutput{Diagnostics: diags}
}

// holdsNode reports whether g holds a node.
func holdsNode(g *Graph) bool {
	return g != nil && slices.ContainsFunc(g.Nodes, func(n *Node) bool { return n != nil })
}

// noNodes warns no-nodes for each source of in, as the importer kit warns of a
// source holding nothing an importer drafts from.
func noNodes(in aggrega.ImportInput, f Format) []aggrega.Diagnostic {
	var out []aggrega.Diagnostic
	for _, src := range importer.Sources(in) {
		out = append(out, importer.NoNodes(importer.Named(in, src), f.Name))
	}
	return out
}
