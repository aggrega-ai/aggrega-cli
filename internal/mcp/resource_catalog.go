// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

const (
	// schemeCatalog addresses a catalog entity's authored YAML.
	schemeCatalog = "catalog"

	// catalogMimeType is the media type served for a catalog:// resource. The
	// body is the entity's authored YAML, served verbatim.
	catalogMimeType = "application/yaml"
)

// catalogKindSegments maps a catalog:// URI's leading kind segment to the entity
// kind string the catalog's raw-bytes accessor keys on. The remaining path
// segments are the entity identity within that kind — metadata.name for the
// by-name kinds, the provider/name:version identity for recipes.
var catalogKindSegments = map[string]string{
	"component":      "Component",
	"feature":        "Feature",
	"recipe":         "Recipe",
	"featurerecipe":  "FeatureRecipe",
	"aggregate":      "Aggregate",
	"schemalibrary":  "SchemaLibrary",
	"mappinglibrary": "MappingLibrary",
	"plugin":         "Plugin",
}

// catalogResourceTemplate returns the catalog-entity URI-template family. The
// template is parameterised by the entity kind segment and the entity identity;
// the instance set is the resolved workspace's catalog, so the family is
// templated rather than pre-enumerated.
func catalogResourceTemplate() resourceTemplate {
	return resourceTemplate{
		URITemplate: "catalog://{kind}/{name}",
		Name:        "catalog-entity",
		Description: "A catalog entity's authored YAML, served verbatim from the resolved workspace catalog. The first path segment is the entity kind (component, feature, recipe, featurerecipe, aggregate, schemalibrary, mappinglibrary, plugin) and the remainder is the entity identity — for example catalog://component/core/compute/api-server or catalog://recipe/helm/mariadb-bitnami:1.0.0.",
		MimeType:    catalogMimeType,
	}
}

// readCatalogResource serves catalog://{kind}/{name} as the entity's authored
// YAML, resolved through the shared catalog loader so the bytes are the
// single-sourced authored content and the workspace resolution matches a CLI
// build of the same tree. The load is tolerant — a broken entity elsewhere in
// the catalog is skipped, not fatal. An unrecognised kind segment, an empty
// identity, or an entity absent from the workspace is a well-formed
// resource-not-found; a workspace resolution or load failure is an internal
// error.
func (s *server) readCatalogResource(uri string) (any, *rpcError) {
	_, body, ok := splitScheme(uri)
	if !ok {
		return nil, resourceNotFound(uri)
	}
	seg, identity, found := strings.Cut(strings.Trim(body, "/"), "/")
	if !found || identity == "" {
		return nil, resourceNotFound(uri)
	}
	kind, ok := catalogKindSegments[seg]
	if !ok {
		return nil, resourceNotFound(uri)
	}
	cat, rerr := s.loadWorkspaceCatalog()
	if rerr != nil {
		return nil, rerr
	}
	data, ok := cat.EntityBytes(kind, identity)
	if !ok {
		return nil, resourceNotFound(uri)
	}
	return readResourceResult{
		Contents: []resourceContents{{URI: uri, MimeType: catalogMimeType, Text: string(data)}},
	}, nil
}

// loadWorkspaceCatalog resolves the server's workspace and runs a tolerant
// Pass-1 catalog load over its catalog search stack, offline. It is the same
// single-sourced reader the catalog-facing tools use; a resolution or load
// failure surfaces as an internal error the caller can report.
func (s *server) loadWorkspaceCatalog() (*catalog.Catalog, *rpcError) {
	tw, err := s.resolveToolWorkspace("", nil)
	if err != nil {
		return nil, &rpcError{Code: codeInternalError, Message: "resolve workspace: " + err.Error()}
	}
	cat, _, err := catalog.LoadPassOneTolerant(tw.root, tw.cacheDir, tw.dirs...)
	if err != nil {
		return nil, &rpcError{Code: codeInternalError, Message: "load catalog: " + err.Error()}
	}
	return cat, nil
}
