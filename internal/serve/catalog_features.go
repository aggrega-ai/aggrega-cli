// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// Entity origin values. They name where the daemon read an entity from, and
// they are the field that keeps a widened answer readable: an entity that
// came through catalog resolution has no workspace file, so without an
// explicit origin a client could not tell "this entity has no file" from
// "this build did not fill the file in".
//
// A catalog-resolved entity reports an EMPTY file rather than the path it was
// read from, and that is a disclosure boundary rather than an omission. The
// configured catalog directories are resolved to absolute paths when the
// workspace config loads, so reporting one would put an absolute host path on
// a wire an editor extension reads — a path that means nothing on the client's
// side of the connection and discloses the layout of the machine the daemon
// runs on. The name is what addresses a catalog entity anyway: it is the
// coordinate a Recipe, an Aggregate and the catalog lookup itself all use.
const (
	// EntityOriginWorkspace marks an entity read from a document inside the
	// workspace root. Its file is the workspace-relative path of that document.
	EntityOriginWorkspace = "workspace"
	// EntityOriginCatalog marks an entity resolved from the configured catalog
	// directories rather than from a workspace document. Its file is empty.
	EntityOriginCatalog = "catalog"
)

// catalogFeatures resolves every Feature the configured catalog directories
// declare, skipping any name that already reached the caller from a workspace
// document.
//
// # Why the workspace wins a name collision
//
// The workspace root is frequently itself one of the configured catalog
// directories, so a Feature authored in the workspace is commonly reachable
// through both paths and would otherwise be reported twice — once with its
// file and once without. Reporting the workspace copy alone is not merely
// deduplication: the workspace document is the one the author is editing, so
// it is the copy whose file a client can open, and it is the copy the
// catalog's own first-win precedence resolves the name to when the workspace
// root leads the search stack.
//
// # It reads the filesystem and reaches no network
//
// Enumeration is over the catalog directory index alone, which is a walk of
// configured directories. Each name is then resolved through the one per-URI
// lookup, and because the name came from that index the lookup is served by
// the directory read that produced it rather than by the cache or a registry.
// So this collects what is already on disk and nothing else — a request
// asking what may attach never becomes the thing that fetches.
//
// # Ordering
//
// The enumeration is sorted, and resolution preserves that order, so the
// collected slice is a function of the catalog's content rather than of the
// order a directory walk happened to return entries in.
func (s *Server) catalogFeatures(alreadyReported map[string]bool) []types.Feature {
	return catalogFeaturesFrom(s.lazyCatalog, alreadyReported)
}

// catalogFeaturesFrom is the whole of catalogFeatures with the lookup supplied
// rather than read off a session, so a caller holding a lookup and no session
// collects the same Features under the same collision rule.
func catalogFeaturesFrom(view *catalog.LazyLookup, alreadyReported map[string]bool) []types.Feature {
	if view == nil {
		return nil
	}
	names := view.EntityNames("Feature")
	out := make([]types.Feature, 0, len(names))
	for _, name := range names {
		if alreadyReported[name] {
			continue
		}
		feat, ok := view.Feature(name)
		if !ok {
			// The name came out of the directory index, so a miss here means
			// the document behind it could not be parsed as a Feature. That is
			// the same direction every other surface takes an unreadable
			// declaration: it is not offered, rather than offered unchecked.
			continue
		}
		out = append(out, feat)
	}
	return out
}

// catalogFeatureRef is the joinable identity a catalog-resolved Feature is
// reported under, on every surface that reports one. It is one construction
// rather than a shape each caller assembles, so the entity index and a
// candidate list cannot disagree about how the same Feature is addressed.
func catalogFeatureRef(feat types.Feature) SourcedEntityRef {
	return SourcedEntityRef{
		WorkspaceEntityRef: WorkspaceEntityRef{
			Kind: "Feature",
			Name: feat.Metadata.Name,
			File: "",
		},
		Origin: EntityOriginCatalog,
	}
}
