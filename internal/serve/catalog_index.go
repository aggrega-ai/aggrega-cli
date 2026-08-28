// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"sort"

	"go.yaml.in/yaml/v3"
)

// CatalogEntity is one workspace-local entity in the aggrega/catalogIndex
// response. Every field is read straight out of the authored document —
// the index reports what the author wrote and derives nothing from it.
//
// Each field is always present on the wire, carrying its empty value
// when the document does not declare it, so a client distinguishes "the
// document declares no description" from "this build sends no
// description field" without guessing. Labels for that reason arrive as
// an empty object rather than as null when a document declares none.
//
// Labels are reported verbatim and are not interpreted. A label is
// opaque metadata to the index: it reports the pairs an author wrote and
// classifies nothing by them.
type CatalogEntity struct {
	Kind        string            `json:"kind"`
	Name        string            `json:"name"`
	APIVersion  string            `json:"apiVersion"`
	Description string            `json:"description"`
	Labels      map[string]string `json:"labels"`
	File        string            `json:"file"`
}

// catalogIndexResult is the aggrega/catalogIndex response: every
// workspace-local entity the daemon can see, in a stable order.
//
// The collection is always an array on the wire, empty when the
// workspace declares no entities, so a client iterates the result
// without a null check.
type catalogIndexResult struct {
	Entities []CatalogEntity `json:"entities"`
}

// dispatchCatalogIndex returns the workspace's entity index — kind,
// name, apiVersion, description, labels and source file for every
// aggrega document under the workspace root. An editor building a
// palette of the entities available to drag onto a canvas asks for it
// here rather than walking the workspace itself, so its idea of what the
// workspace contains matches the daemon's exactly.
//
// Takes no params: the index is a property of the daemon's workspace
// root, which it resolved at startup. Read-only — it mutates nothing,
// resolves nothing, and reaches no registry or network.
func (s *Server) dispatchCatalogIndex(ctx context.Context, req *jsonRPCRequest) error {
	if err := ctx.Err(); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInternalError, err.Error())
	}
	return s.writeResult(ctx, req.ID, catalogIndexResult{Entities: s.catalogIndex(ctx)})
}

// catalogIndex projects the workspace document traversal down to the
// entity index, in a deterministic order.
//
// A document is an entity here when it declares both a kind: and a
// metadata.name: those two are what make it addressable, and a document
// missing either is not something a caller can refer to afterwards.
// Foreign YAML in the workspace — configuration, a chart's values file —
// declares no kind: and is skipped, which is what keeps this an entity
// index rather than a file listing.
//
// Ordering is the shared workspace-entity order — kind, then name, then
// source file — which the traversal's own delivery order is not. An index
// that leaned on traversal order would report entities in the order their
// file names happen to sort in, and would reorder the moment a file was
// renamed.
func (s *Server) catalogIndex(ctx context.Context) []CatalogEntity {
	out := []CatalogEntity{}
	s.rangeWorkspaceDocs(ctx, func(doc workspaceDoc) bool {
		kind := scalarAtPathOnDoc(doc.node, []string{"kind"})
		if kind == "" {
			return true
		}
		name, _ := metadataNameOf(doc.node)
		if name == "" {
			return true
		}
		rel, inside := relInsideRoot(s.workspaceRoot, doc.path)
		if !inside {
			// The parse cache holds whatever file the editor last sent,
			// which need not be a workspace file at all. An entity
			// outside the root is not workspace-local, and reporting it
			// would also put an absolute host path on the wire.
			return true
		}
		out = append(out, CatalogEntity{
			Kind:        kind,
			Name:        name,
			APIVersion:  scalarAtPathOnDoc(doc.node, []string{"apiVersion"}),
			Description: scalarAtPathOnDoc(doc.node, []string{"metadata", "description"}),
			Labels:      stringMapAtPathOnDoc(doc.node, []string{"metadata", "labels"}),
			File:        rel,
		})
		return true
	})
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		return entityOrderLess(a.Kind, a.Name, a.File, b.Kind, b.Name, b.File)
	})
	return out
}

// stringMapAtPathOnDoc reads the mapping doc declares at the supplied
// key path as string pairs, and returns an empty map when the path
// addresses no mapping. Non-scalar values are skipped: the pairs this
// reads are flat by contract, and a nested value has no string form to
// report.
//
// The returned map is never nil, so a caller marshalling it emits an
// object rather than null.
func stringMapAtPathOnDoc(doc *yaml.Node, path []string) map[string]string {
	out := map[string]string{}
	n := nodeAtPathOnDoc(doc, path)
	if n == nil || n.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, value := n.Content[i], n.Content[i+1]
		if value.Kind != yaml.ScalarNode {
			continue
		}
		out[key.Value] = value.Value
	}
	return out
}
