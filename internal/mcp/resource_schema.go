// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"strings"

	schemas "github.com/aggrega-ai/aggrega-schemas"
)

const (
	// schemeSchema addresses an entity or contract kind's canonical JSON Schema.
	schemeSchema = "schema"

	// schemaMimeType is the media type served for a schema:// resource. The body
	// is the distributed JSON Schema file, byte-identical to the shipped schema.
	schemaMimeType = "application/schema+json"
)

// schemaResourceTemplate returns the entity-schema URI-template family. The
// template is parameterised by the kind token; a schema:// read resolves it to
// the canonical schema for that kind.
func schemaResourceTemplate() resourceTemplate {
	return resourceTemplate{
		URITemplate: "schema://{kind}",
		Name:        "entity-schema",
		Description: "The canonical JSON Schema for an Aggrega entity or contract kind, served byte-identical to the distributed schema file. The kind is a lowercase-hyphenated token (for example schema://component or schema://recipe); an unknown kind is a resource-not-found.",
		MimeType:    schemaMimeType,
	}
}

// schemaResourceList enumerates one concrete resource per addressable schema
// kind, drawn from the embedded schema set. The set is fixed and cheap to
// enumerate, and the kind order is the schema set's own sorted order, so the
// listing is deterministic across runs.
func schemaResourceList() []resource {
	kinds := schemas.Kinds()
	out := make([]resource, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, resource{
			URI:         schemeSchema + "://" + k,
			Name:        k + " schema",
			Description: "Canonical JSON Schema for the " + k + " kind.",
			MimeType:    schemaMimeType,
		})
	}
	return out
}

// readSchemaResource serves schema://{kind} as the canonical JSON Schema for the
// kind, delegating the lookup to the embedded schema set so the served bytes are
// the single-sourced schema, never a restatement. An empty or unknown kind is a
// well-formed resource-not-found.
func readSchemaResource(uri string) (readResourceResult, *rpcError) {
	_, body, ok := splitScheme(uri)
	if !ok {
		return readResourceResult{}, resourceNotFound(uri)
	}
	kind := strings.Trim(body, "/")
	if kind == "" {
		return readResourceResult{}, resourceNotFound(uri)
	}
	schema, ok := schemas.SchemaByKind(kind)
	if !ok {
		return readResourceResult{}, resourceNotFound(uri)
	}
	return readResourceResult{
		Contents: []resourceContents{{URI: uri, MimeType: schemaMimeType, Text: schema}},
	}, nil
}
