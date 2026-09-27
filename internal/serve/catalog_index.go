// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
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
// directories reports an empty path, because a catalog directory is wherever
// the workspace configures it and need not sit under the workspace root at
// all. Every File on this protocol is anchored to that root, which is the only
// anchor the connection gives a client, so there is no value to report here
// that a client could resolve — whether the configured directory was spelled
// absolutely or relatively. The origin is what makes that empty value readable
// instead of ambiguous — it says the entity has no workspace file, rather than
// leaving a client to wonder whether one was simply not filled in.
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

// dispatchCatalogIndex returns the entity index of the workspace the request
// named — kind, name, apiVersion, description, labels and source file for every
// aggrega document under that root. An editor building a palette of the entities
// available to drag onto a canvas asks for it here rather than walking the
// workspace itself, so its idea of what the workspace contains matches the
// daemon's exactly.
//
// The index is a property of one workspace, so the request names which. Two
// requests naming two workspaces are answered from two trees, and neither
// entity set reaches the other. Read-only — it mutates nothing, resolves
// nothing, and reaches no registry or network.
func (s *Server) dispatchCatalogIndex(ctx context.Context, req *jsonRPCRequest) error {
	// A method with no parameters of its own still decodes, so a field a caller
	// sent is refused rather than dropped. See noParams for why an answer that
	// ignored an argument is worse than a refusal naming it.
	if err := decodeParams(req, &noParams{}); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}

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
// missing either is not something a caller can refer to afterwards. A
// document that declares an apiVersion in another project's group is
// skipped whatever else it carries, so a rendered Kubernetes manifest —
// which declares a kind: and a metadata.name of its own — is not reported
// as something this workspace holds. Those two rules together are what
// keep this an entity index rather than a file listing: the first drops
// YAML that addresses nothing, the second drops documents that address
// something this compiler does not read.
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
// Only Features are drawn from the catalog, because Features are the only
// population a candidate surface reports from it as an identity a client joins
// against this index. A completion candidate is not such an identity: it is a
// name the author types into a document, so the Components completion offers
// from the catalog directories need no entry here, and listing them would fill
// a palette built from this index with every Component a standard library
// declares. That is a bound on what has a consumer, not a claim that other
// kinds are unreachable.
//
// Ordering is the shared workspace-entity order — kind, then name, then
// source file — which the traversal's own delivery order is not. An index
// that leaned on traversal order would report entities in the order their
// file names happen to sort in, and would reorder the moment a file was
// renamed. A catalog-resolved entity sorts under an empty file, so it orders
// ahead of a workspace document sharing its kind and name rather than
// landing in an order that depends on where the daemon happened to read it.
func (s *Server) catalogIndex(ctx context.Context) []CatalogEntity {
	return workspaceEntityIndex(ctx, s.workspaceRoot, s.lazyCatalog, s.overlayDocs())
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
		rel, inside := pathscope.Relative(root, abs)
		if !inside {
			continue
		}
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

// LockfilesFor maps each Release path to the path of its lockfile, placed by
// [lockfile.PathFor] — the rule every command that writes or reads a lockfile
// applies — so an answer carrying it names the file a pull writes and a build
// or a graph request reads, and no client keeps a copy of the rule.
//
// Each lockfile path is in the coordinate its Release was given in: absolute for
// an absolute Release, relative to the same root for a relative one, because the
// rule reads only the path's name. A caller therefore looks a Release up under
// exactly the string it received the Release as.
//
// The map is never nil, and it is filled and never ranged over. It reaches the
// wire through the JSON encoder, which writes a map's keys in sorted order, so
// the answer is byte-identical for identical input.
func LockfilesFor(releases []string) map[string]string {
	out := make(map[string]string, len(releases))
	for _, release := range releases {
		out[release] = lockfile.PathFor(release)
	}
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
		apiVersion := scalarAtPathOnDoc(doc.node, []string{"apiVersion"})
		if declaresForeignAPIGroup(apiVersion) {
			return true
		}
		name, _ := metadataNameOf(doc.node)
		if name == "" {
			return true
		}
		rel, inside := pathscope.Relative(root, doc.path)
		if !inside {
			// The parse cache holds whatever file the editor last sent,
			// which need not be a workspace file at all. A document outside
			// the root is not workspace-local, so it is not a member of the
			// population this half of the index reports — which is a
			// statement about membership and not about the path, since
			// entities that legitimately live outside the root reach the
			// index through catalog resolution below.
			return true
		}
		if kind == "Feature" {
			workspaceFeatures[name] = true
		}
		out = append(out, CatalogEntity{
			Kind:        kind,
			Name:        name,
			APIVersion:  apiVersion,
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

// aggregaAPIGroup is the apiVersion group an aggrega document declares. The
// group alone, without a version: what a reader of it asks is whose document
// this is, and every version of the vocabulary answers that the same way.
const aggregaAPIGroup = "aggrega.ai"

// declaresForeignAPIGroup reports whether an apiVersion positively names a group
// other than aggrega.ai or a subgroup of it.
//
// The test is negative, and the absent case is the reason. A document declaring
// no apiVersion at all is not foreign and is admitted: an aggrega document whose
// apiVersion is missing is still an aggrega document, one the catalog resolves
// and validation has a diagnostic to describe. Reading absence as foreign would
// drop it from the index while every surface that resolves by name still found
// it, which reports a workspace nobody authored — where admitting it reports a
// document an author can then be told is incomplete.
//
// It enumerates no kinds, and that is what separates it from a list of entity
// kinds written beside the index. Such a list has to be remembered when a kind
// is added and stops reporting the new one silently when it is not, which is a
// wrong answer indistinguishable from a right one; a group test admits a kind
// added tomorrow with no edit here. The same reasoning rules out keying this on
// a closed set of known apiVersion values, which would drop every document of a
// version the set had not yet been taught.
//
// An apiVersion carrying no "/" declares a version in no group at all — the
// shape a core Kubernetes object takes — and no group is not this one.
//
// This is deliberately not peekDocumentHeader's non-aggrega guard, and the two
// must not be unified. That guard answers which schema validates a buffer, so
// it can only admit a document that positively names one and reads an absent
// apiVersion as unclassifiable. This answers which documents a workspace holds,
// where an unclassifiable document is exactly the one worth reporting. The two
// share the group name above and nothing else.
func declaresForeignAPIGroup(apiVersion string) bool {
	if apiVersion == "" {
		return false
	}
	group, _, _ := strings.Cut(apiVersion, "/")
	return group != aggregaAPIGroup && !strings.HasSuffix(group, "."+aggregaAPIGroup)
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
