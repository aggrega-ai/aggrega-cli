// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"path/filepath"
	"sort"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
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
//
// Origin names where the entity was read from, and File is read together
// with it rather than alone. A workspace document reports its
// workspace-relative path; an entity resolved from the configured catalog
// directories reports an empty path, because the only path it has is an
// absolute host path that would mean nothing on the client's side of the
// connection. The origin is what makes that empty value readable instead of
// ambiguous — it says the entity has no workspace file, rather than leaving a
// client to wonder whether one was simply not filled in.
type CatalogEntity struct {
	Kind        string            `json:"kind"`
	Name        string            `json:"name"`
	APIVersion  string            `json:"apiVersion"`
	Description string            `json:"description"`
	Labels      map[string]string `json:"labels"`
	File        string            `json:"file"`
	Origin      string            `json:"origin"`
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
// # The index spans both places an entity can come from
//
// The workspace documents, and the Features the configured catalog
// directories declare. The second population is here because this index is
// what a client joins a candidate against: a surface that offered a
// catalog-resolved Feature as a candidate while the index knew nothing about
// it would hand the client an identity it could not resolve to anything
// displayable. The two therefore widen together or not at all.
//
// Only Features are drawn from the catalog today, because Features are the
// only population the candidate surfaces resolve from it. That is a bound on
// what has a consumer, not a claim that other kinds are unreachable.
//
// Ordering is the shared workspace-entity order — kind, then name, then
// source file — which the traversal's own delivery order is not. An index
// that leaned on traversal order would report entities in the order their
// file names happen to sort in, and would reorder the moment a file was
// renamed. A catalog-resolved entity sorts under an empty file, so it orders
// ahead of a workspace document sharing its kind and name rather than
// landing in an order that depends on where the daemon happened to read it.
func (s *Server) catalogIndex(ctx context.Context) []CatalogEntity {
	var overlay []workspaceDoc
	s.parseCache.Range(func(file string, node *yaml.Node, _ []byte) bool {
		overlay = append(overlay, workspaceDoc{path: filepath.Clean(file), node: node})
		return true
	})
	return workspaceEntityIndex(ctx, s.workspaceRoot, s.lazyCatalog, overlay)
}

// WorkspaceEntityIndex reports every entity a workspace holds — each one's kind,
// name, apiVersion, description, labels and source file — for a caller that
// holds a workspace root and a catalog lookup rather than an open session.
//
// It is the same projection the daemon answers with, over the same traversal and
// the same rule for what counts as an entity, so two surfaces reporting a
// workspace's entities cannot come to disagree about what is in it. What a
// caller without a session does not supply is the overlay of documents an editor
// is holding unsaved; with none, the answer is read from what is on disk, which
// is what such a caller can see anyway.
//
// Read-only: it resolves nothing, writes nothing, and reaches no registry or
// network. The collection is never nil, so a caller iterates it without a nil
// check.
func WorkspaceEntityIndex(ctx context.Context, root string, view *catalog.LazyLookup) []CatalogEntity {
	return workspaceEntityIndex(ctx, root, view, nil)
}

// ReleaseCandidates reports every Release-kind document under root, decided by
// each file's own kind header rather than by its name — so a Release named
// without a conventional prefix is included and a non-Release file named like
// one is not.
//
// It is the same walk that answers the question for an editor, so which files
// count as Releases is decided once. What differs is the coordinate each path is
// reported under: this reports the path relative to the root, which is the
// coordinate WorkspaceEntityIndex reports a file under, so a caller that asked
// both questions can line the two answers up. A caller that needs an absolute
// path joins the root back on, which it supplied.
//
// The paths are sorted, and the collection is never nil.
func ReleaseCandidates(root string) []string {
	out := []string{}
	for _, abs := range walkForReleases(root) {
		rel, inside := relInsideRoot(root, abs)
		if !inside {
			continue
		}
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

// workspaceEntityIndex is the whole of the entity index with the root, the
// catalog lookup and the fresher-than-disk overlay supplied rather than read off
// a session, so the session-bound method and the sessionless entry point above
// are one implementation rather than two that agree today.
func workspaceEntityIndex(ctx context.Context, root string, view *catalog.LazyLookup, overlay []workspaceDoc) []CatalogEntity {
	out := []CatalogEntity{}
	workspaceFeatures := map[string]bool{}
	rangeWorkspaceDocsIn(ctx, root, overlay, func(doc workspaceDoc) bool {
		kind := scalarAtPathOnDoc(doc.node, []string{"kind"})
		if kind == "" {
			return true
		}
		name, _ := metadataNameOf(doc.node)
		if name == "" {
			return true
		}
		rel, inside := relInsideRoot(root, doc.path)
		if !inside {
			// The parse cache holds whatever file the editor last sent,
			// which need not be a workspace file at all. A document outside
			// the root is not workspace-local, and reporting it under a
			// workspace-relative path would misstate where it came from.
			// Entities that legitimately live outside the root reach the
			// index through catalog resolution below, addressed by name and
			// disclosing no host path.
			return true
		}
		if kind == "Feature" {
			workspaceFeatures[name] = true
		}
		out = append(out, CatalogEntity{
			Kind:        kind,
			Name:        name,
			APIVersion:  scalarAtPathOnDoc(doc.node, []string{"apiVersion"}),
			Description: scalarAtPathOnDoc(doc.node, []string{"metadata", "description"}),
			Labels:      stringMapAtPathOnDoc(doc.node, []string{"metadata", "labels"}),
			File:        rel,
			Origin:      EntityOriginWorkspace,
		})
		return true
	})
	for _, feat := range catalogFeaturesFrom(view, workspaceFeatures) {
		labels := map[string]string{}
		for k, v := range feat.Metadata.Labels.All() {
			labels[k] = v
		}
		ref := catalogFeatureRef(feat)
		out = append(out, CatalogEntity{
			Kind:        ref.Kind,
			Name:        ref.Name,
			APIVersion:  feat.APIVersion,
			Description: feat.Metadata.Description,
			Labels:      labels,
			File:        ref.File,
			Origin:      ref.Origin,
		})
	}
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
