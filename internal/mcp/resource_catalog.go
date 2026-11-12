// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
)

const (
	// schemeCatalog addresses a catalog entity's authored YAML.
	schemeCatalog = "catalog"

	// catalogMimeType is the media type served for a catalog:// resource. The
	// body is the entity's authored YAML, served verbatim.
	catalogMimeType = "application/yaml"
)

// catalogKindForSegment resolves a catalog:// URI's leading kind segment to the
// entity kind string the catalog's raw-bytes accessor keys on, and reports
// whether the segment names an entity kind at all. The remaining path segments
// are the entity identity within that kind — metadata.name for the by-name kinds,
// the provider/name:version identity for recipes.
//
// It resolves every entity kind rather than a narrower list of the kinds a
// catalog is expected to hold. Whether a given kind is actually resident is the
// catalog's own answer, given when the identity is looked up, so keeping a second
// list here would only be a chance for the two to disagree — and when they did,
// the newer kind was the one left unaddressable.
func catalogKindForSegment(segment string) (string, bool) {
	kind, ok := entitykind.BySegment(segment)
	return string(kind), ok
}

// catalogResourceTemplate returns the catalog-entity URI-template family. The
// template is parameterised by the entity kind segment and the entity identity;
// the instance set is the resolved workspace's catalog, so the family is
// templated rather than pre-enumerated.
//
// The description names the kind segments by reading the same vocabulary the
// resolution reads, so what a reader is told and what actually resolves cannot
// come apart.
func catalogResourceTemplate() resourceTemplate {
	return resourceTemplate{
		URITemplate: "catalog://{kind}/{name}",
		Name:        "catalog-entity",
		Description: "A catalog entity's authored YAML, served verbatim from the resolved workspace catalog. The first path segment is the entity kind (" +
			strings.Join(entitykind.Segments(), ", ") +
			") and the remainder is the entity identity — for example catalog://component/core/compute/api-server or catalog://recipe/helm/mariadb-bitnami:1.0.0",
		MimeType: catalogMimeType,
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
//
// An entity absent because the one catalog file declaring it was refused at load
// is still a resource-not-found, and its message names that file and why it was
// refused instead of only the URI: the entity does exist in the workspace, and
// the refusal is what the reader has to fix. The file is singled out by
// compilerops.SoleRefusal, the question a build and the daemon ask of the same
// record, so all three single out the same file; and the message is
// compilerops.RefusedReferenceLine, the one-line form the daemon answers an
// unresolved name with, so a connection reads it in the same words from either.
// Where no refused file declares the identity, or more than one does, the answer
// is the plain not-found it would be with nothing refused.
func (s *server) readCatalogResource(uri string) (readResourceResult, *rpcError) {
	_, body, ok := splitScheme(uri)
	if !ok {
		return readResourceResult{}, resourceNotFound(uri)
	}
	seg, identity, found := strings.Cut(strings.Trim(body, "/"), "/")
	if !found || identity == "" {
		return readResourceResult{}, resourceNotFound(uri)
	}
	kind, ok := catalogKindForSegment(seg)
	if !ok {
		return readResourceResult{}, resourceNotFound(uri)
	}
	loaded, rerr := s.loadWorkspaceCatalog()
	if rerr != nil {
		return readResourceResult{}, rerr
	}
	data, ok := loaded.catalog.EntityBytes(kind, identity)
	if !ok {
		return readResourceResult{}, loaded.notFound(uri, kind, identity)
	}
	return readResourceResult{
		Contents: []resourceContents{{URI: uri, MimeType: catalogMimeType, Text: string(data)}},
	}, nil
}

// workspaceCatalog is the catalog a tolerant load read out of the resolved
// workspace, with the record of the files that load refused and the workspace
// the load read, which spells a refused file's location as this call may be
// told it.
type workspaceCatalog struct {
	catalog *catalog.Catalog
	refused []catalog.SkippedEntity
	tw      *toolWorkspace
}

// notFound is the resource-not-found answer for uri, whose kind and identity the
// catalog holds no entity under. It is built by resourceNotFound in every case,
// so the code is the one that function builds; where exactly one refused catalog
// file declares that identity, the message is that refusal instead of the plain
// not-found.
func (w workspaceCatalog) notFound(uri, kind, identity string) *rpcError {
	notFound := resourceNotFound(uri)
	if refusal, found := compilerops.SoleRefusal(w.refused, identity, compilerops.KindHintFor(kind)); found {
		notFound.Message = w.tw.reportedText(compilerops.RefusedReferenceLine(refusal.Path, refusal.Err, uri))
	}
	return notFound
}

// loadWorkspaceCatalog resolves the server's workspace and runs a tolerant
// Pass-1 catalog load over its catalog search stack, offline, keeping the
// record of the files the load refused so an answer about an entity one of them
// declares can name it. A resolution or load failure, and the resolver's refusal
// of the stack, surface as an internal error the caller can report.
func (s *server) loadWorkspaceCatalog() (workspaceCatalog, *rpcError) {
	tw, err := s.resolveToolWorkspace("", nil)
	if err != nil {
		return workspaceCatalog{}, &rpcError{Code: codeInternalError, Message: err.Error()}
	}
	dirs, err := tw.catalogDirsFor("")
	if err != nil {
		return workspaceCatalog{}, &rpcError{Code: codeInternalError, Message: err.Error()}
	}
	named, _ := tw.namedCatalogDirsFor("")
	cat, refused, err := catalog.LoadPassOneTolerantWithNamedDirs(tw.root, tw.cacheDir, named, dirs...)
	if err != nil {
		return workspaceCatalog{}, &rpcError{Code: codeInternalError, Message: "load catalog: " + err.Error()}
	}
	return workspaceCatalog{catalog: cat, refused: refused, tw: tw}, nil
}
