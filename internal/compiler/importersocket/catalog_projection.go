// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importersocket

import (
	"maps"
	"slices"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// ProjectCatalog builds the view of a catalog's Components an importer is
// handed on its input: one entry per Component name the catalog holds, at the
// version a reference naming that Component without a version resolves to.
//
// Each entry carries the Component's name and version, its labels, the top-level
// key names its values and provides pillars declare, and its usesSchema sockets
// with their selectors. It carries metadata the catalog already holds and
// nothing else: no value, no default and no secret is read, and nothing is
// fetched — the projection is a pure function of the catalog view handed in, so
// the import stays ahead-of-time. The host reads no label, key or selector for
// what it means; it copies them.
//
// An extending Component is projected with its chain resolved through the
// catalog's own extends merge, so it carries the labels and sockets it inherits
// and its base's pillars, as every reader of a catalog Component sees it. The
// resolution reads the catalog and never writes to it. A Component whose chain
// does not resolve is projected as authored; the fault is the catalog's to
// report, on the surfaces that validate it, rather than the import's.
//
// The order is fixed so one catalog yields one byte sequence: entries sorted by
// name, key names sorted, sockets in the declaration order the catalog's merge
// gives them, and labels left to the codec, which writes a map's keys sorted.
//
// It returns nil when the catalog holds no Component, so a payload built from an
// empty catalog omits the field and is byte-identical to one built with none.
func ProjectCatalog(cat *catalog.Catalog) *abi.CatalogProjection {
	if cat == nil || len(cat.Components) == 0 {
		return nil
	}

	// One entry per name: the index holds every version of a name under its own
	// key, and the version select below chooses among them.
	names := map[string]struct{}{}
	for _, c := range cat.Components {
		names[c.Metadata.Name] = struct{}{}
	}

	lookup := cat.CatalogLookup()
	schemaLibraries := cat.DefaultLibraries().Schemas
	comps := make([]abi.CatalogComponent, 0, len(names))
	for _, name := range slices.Sorted(maps.Keys(names)) {
		comp, ok := lookup.Component(name)
		if !ok {
			continue
		}
		if comp.Spec.Extends != "" {
			if resolved, errs := validator.ResolveComponentExtends(comp, lookup.Component); len(errs) == 0 {
				comp = resolved
			}
		}
		comps = append(comps, projectComponent(comp, schemaLibraries))
	}
	if len(comps) == 0 {
		return nil
	}
	return &abi.CatalogProjection{Components: comps}
}

// projectComponent copies one resolved Component's metadata into its projected
// entry.
func projectComponent(comp types.Component, libraries map[string]types.SchemaLibrary) abi.CatalogComponent {
	out := abi.CatalogComponent{
		Name:     comp.Metadata.Name,
		Version:  comp.Metadata.Version,
		Values:   sortedNames(resolver.ComponentValuesKeys(comp, libraries)),
		Provides: sortedNames(resolver.ComponentProvidesKeys(comp, libraries)),
	}
	for k, v := range comp.Metadata.Labels.All() {
		if out.Labels == nil {
			out.Labels = map[string]string{}
		}
		out.Labels[k] = v
	}
	for name, socket := range comp.Spec.UsesSchema.All() {
		out.Sockets = append(out.Sockets, abi.CatalogSocket{Name: name, Selector: socket.Selector})
	}
	return out
}

// sortedNames returns a key set's names in sorted order, and nil for an empty
// set so the field is omitted from the wire.
func sortedNames(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(set))
}
