// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
)

// describeArgs is the describe tool's argument shape. Exactly one of name or
// release selects the subject.
type describeArgs struct {
	Name           string   `json:"name"`
	Release        string   `json:"release"`
	WorkspaceRoot  string   `json:"workspace_root"`
	CatalogDirs    []string `json:"catalog_dirs"`
	ResponseFormat string   `json:"response_format"`
}

// describeEntityPayload is the structured payload for a catalog entity subject:
// its kind, its identity, and its resolved definition.
//
// Kind is whichever kind the name resolved to, read off the resolved entity
// rather than assumed from the request — a caller that names a Recipe is
// answered about a Recipe.
type describeEntityPayload struct {
	Kind       string          `json:"kind"`
	Name       string          `json:"name"`
	Version    string          `json:"version"`
	Definition json.RawMessage `json:"definition"`
}

// describeReleasePayload is the structured payload for a Release subject: the
// resolved Release/Manifest/Aggregate identities and the entities its catalog
// closure reaches.
type describeReleasePayload struct {
	Release     string       `json:"release"`
	Aggregate   string       `json:"aggregate"`
	Manifest    string       `json:"manifest"`
	Environment string       `json:"environment"`
	EntityCount int          `json:"entity_count"`
	Entities    []closureRef `json:"entities"`
	// MappingDiagnostics names each Manifest mapping whose target recipe does not
	// implement the mapped type. This tool reports on a Release rather than
	// producing anything from it, so a fault is described alongside the closure
	// rather than replacing the description with a refusal: a caller asking what a
	// Release contains is answered, and told what is wrong with it.
	MappingDiagnostics []string `json:"mapping_diagnostics,omitempty"`
	// Deprecations names each pinned reference carrying a recorded deprecation
	// marker, from the shared report the build reads — so a caller asking what a
	// Release depends on is told which of those dependencies has been deprecated,
	// rather than having to build it to find out. This tool describes rather than
	// produces, so the entries are informational and never a refusal.
	Deprecations []string `json:"deprecations,omitempty"`
}

var describeToolSpec = toolSpec{
	kinds: []string{"component", "feature", "release"},
	description: "Describe an Aggrega subject in structured form, rendering nothing and writing nothing. Two modes: pass `name` to resolve a " +
		"catalog entity of any kind by its type URI and get its fully-resolved definition (schema, uses-schema, provides, " +
		"labels) with inheritance applied; or pass `release` (a Release path) to get its dependency closure — what a " +
		"deployment is built FROM, not its shape — the resolved Aggregate and Manifest identities plus every catalog " +
		"entity the Release reaches, with resolved versions. Use `name` to understand a building block before wiring it " +
		"into an Aggregate, and `release` to see what a deployment depends on before building it. Optional " +
		"`workspace_root` and `catalog_dirs` override discovery; `response_format` chooses detailed (full definition / " +
		"every entity) or concise (identity and counts).",
	inputSchema: `{
  "type": "object",
  "properties": {
    "name": {
      "type": "string",
      "minLength": 1,
      "description": "A catalog entity type URI (any kind) to resolve and describe, e.g. core/compute/api-server. Mutually exclusive with release."
    },
    "release": {
      "type": "string",
      "minLength": 1,
      "description": "A Release path whose dependency closure to report. Mutually exclusive with name."
    },
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. Defaults to discovery from the server's working directory."
    },
    "catalog_dirs": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Optional catalog directories, prepended (first-match-wins) to the workspace catalog search stack."
    },
    "response_format": {
      "type": "string",
      "enum": ["detailed", "concise"],
      "description": "detailed returns the full definition or every closure entity; concise returns identity and counts. Defaults to detailed."
    }
  },
  "anyOf": [
    {"required":["name"],"not":{"required":["release"]}},
    {"required":["release"],"not":{"required":["name"]}}
  ]
}`,
	invoke: describeInvoke,
}

func describeInvoke(s *server, _ context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a describeArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments("describe", err)
		}
	}
	name := strings.TrimSpace(a.Name)
	release := strings.TrimSpace(a.Release)
	switch {
	case name != "" && release != "":
		return errorResult("describe takes exactly one of 'name' or 'release', not both")
	case name == "" && release == "":
		return errorResult("describe requires either a 'name' (catalog entity) or a 'release' (path)")
	case name != "":
		return s.describeCatalogEntity(a, name)
	default:
		return s.describeRelease(a, release)
	}
}

// describeCatalogEntity resolves a name by its type URI through the
// request-scoped view — with extends inheritance applied — and returns the
// fully-resolved definition of whichever kind it matched.
//
// The resolution is the shared one every by-name surface runs, so a name that
// resolves at a command line or over the daemon's protocol resolves here too, to
// the same entity and the same kind. A name that resolves to nothing is refused
// rather than answered with an empty definition: an empty answer would report a
// mistyped name as an entity with nothing to say about itself.
func (s *server) describeCatalogEntity(a describeArgs, name string) (*callToolResult, *rpcError) {
	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, a.CatalogDirs)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace for the catalog lookup.", err, "component"))
	}
	view := tw.newView(nil)

	match, ok := compilerops.ResolveEntity(view, name)
	if !ok {
		return errorResult(explainDiagnostic(fmt.Sprintf(
			"No catalog entity named %q resolved, in any kind (%s). Check the type URI and that the "+
				"catalog directories cover it.",
			name, strings.Join(compilerops.EntityKinds(), ", ")), "component"))
	}
	def, mErr := json.Marshal(match.Definition)
	if mErr != nil {
		return nil, &rpcError{Code: codeInternalError,
			Message: "marshal " + strings.ToLower(match.Kind) + ": " + mErr.Error()}
	}
	payload := describeEntityPayload{
		Kind: match.Kind, Name: match.Name, Version: match.Version, Definition: def,
	}
	return textResult(renderDescribeEntityText(payload, match.Description), payload)
}

// describeRelease resolves a Release's catalog closure and returns a summary of
// the entities it reaches.
func (s *server) describeRelease(a describeArgs, release string) (*callToolResult, *rpcError) {
	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, a.CatalogDirs)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace for the Release.", err, "release"))
	}
	releasePath := tw.absWorkspacePath(release)
	lf := loadReleaseLockfile(releasePath)
	view := tw.newView(lf)

	resolved, cerr := compilerops.ResolveClosure(compilerops.ClosureInputs{
		View:            view,
		ReleasePath:     releasePath,
		WorkspaceRoot:   tw.root,
		WorkspaceConfig: tw.cfg,
		Lockfile:        lf,
		Augment:         true,
	})
	if cerr != nil {
		return errorResult(explainToolError(
			"Could not resolve the Release closure to describe it.", cerr, closureStageKind(cerr.Stage)))
	}

	detail := parseResponseFormat(a.ResponseFormat) == responseFormatDetailed
	payload := describeReleasePayload{
		Release:     releasePath,
		Aggregate:   resolved.Aggregate.Metadata.Name,
		Manifest:    resolved.Manifest.Metadata.Name,
		Environment: resolved.Release.Metadata.Environment,
		EntityCount: len(resolved.Closure.Entities),
	}
	for _, f := range resolved.ManifestConsistencyFindings {
		payload.MappingDiagnostics = append(payload.MappingDiagnostics, f.Error())
	}
	for _, f := range lf.DeprecationFindings() {
		payload.Deprecations = append(payload.Deprecations, f.Notice())
	}
	if detail {
		for _, e := range resolved.Closure.Entities {
			// The entity axis mixes kinds, so it states none and each reference
			// carries its own hint. The kind is rendered as an author writes it
			// rather than as the walk recorded it, through the shared projection
			// every tool here reports a resolved reference through — so one entry
			// reads the same whichever tool returned it.
			payload.Entities = append(payload.Entities, closureRefOf(e.URI, e.ResolvedVersion, "", e.KindHint))
		}
	}
	return textResult(renderDescribeReleaseText(payload), payload)
}

// renderDescribeEntityText renders the human-readable one-line summary of a
// catalog entity, with its description when it carries one.
func renderDescribeEntityText(p describeEntityPayload, description string) string {
	line := fmt.Sprintf("%s %s (version %s)", p.Kind, p.Name, p.Version)
	if strings.TrimSpace(description) != "" {
		line += ": " + description
	}
	return line
}

// renderDescribeReleaseText renders the human-readable Release closure summary.
func renderDescribeReleaseText(p describeReleasePayload) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Release %s\n", p.Release)
	fmt.Fprintf(&b, "  aggregate: %s\n", p.Aggregate)
	fmt.Fprintf(&b, "  manifest: %s\n", p.Manifest)
	if p.Environment != "" {
		fmt.Fprintf(&b, "  environment: %s\n", p.Environment)
	}
	fmt.Fprintf(&b, "  closure entities: %d\n", p.EntityCount)
	for _, e := range p.Entities {
		if e.Version != "" {
			fmt.Fprintf(&b, "    - %s (%s)\n", e.URI, e.Version)
		} else {
			fmt.Fprintf(&b, "    - %s\n", e.URI)
		}
	}
	// The mapping faults follow the closure they were found in rather than
	// preceding it, so the description the caller asked for arrives first and is
	// not mistaken for an error report.
	for _, d := range p.MappingDiagnostics {
		fmt.Fprintf(&b, "  mapping: %s\n", d)
	}
	for _, d := range p.Deprecations {
		fmt.Fprintf(&b, "  deprecation: %s\n", d)
	}
	return strings.TrimRight(b.String(), "\n")
}
