// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/validateops"
)

// isSingleEntityDocument reports whether a parsed buffer is a document of a kind
// the single-entity check accepts — a Component, a Feature, a Recipe, a
// FeatureRecipe, a Workload or a Plugin.
//
// The set is the check's own, read from validateops rather than written here, so
// the documents this daemon checks one at a time are exactly the ones the command
// line and the assistant-facing tools check one at a time. An Aggregate is never
// in it, so the Release's own Aggregate is never answered this way; nor are a
// Manifest, a Release, or a library, which the single-entity check refuses on
// every surface and routes to a scope that reads them.
func isSingleEntityDocument(doc *yaml.Node) bool {
	kind, ok := classifyAggregaDocument(doc)
	return ok && slices.Contains(validateops.SingleEntityKinds(), string(kind))
}

// runEntityValidation answers a request naming a Release for a catalog entity
// file: it runs the single-entity check over the editor's buffer in that
// Release's terms and maps what the check reports into this file's diagnostics.
//
// The check is the command line's and the tools' — validateops, handed the
// buffer the request carries and the catalog search stack the workspace
// configuration resolves for the Release (catalogDirsFor) — so the entity's
// references, the Component a Recipe implements and a library a pillar refers to,
// resolve through the directories that Release builds with, and the three
// surfaces reach one verdict on the same inputs. Like theirs it reads no lockfile
// and validates no closure: the subject is the entity, and the Release only says
// which catalog it is read against.
//
// A Release naming no file, or naming a directory, is answered with the sentence
// the other two surfaces refuse it with, on this file, rather than refused as a
// request: this
// handler reports a Release it cannot read as something the author can fix, and
// checking the entity anyway would check it in the workspace's terms while
// answering as though in the Release's.
//
// The check reports one blocking finding at most, in the check's own words — the
// same text the tool reports — drawn where entityCheckFinding places it, and the
// advisory lines it gathers while reading the catalog, which never fail its
// verdict, at the warning tier ahead of it, the order they were decided in. A
// failed assertion is one of the check's warnings rather than its verdict, as the
// check itself decides: it is drawn where entityCheckFinding places it, on the
// assertion's own key where the check states one, at the warning tier and in the
// check's words, after the advisory lines and where a blocking finding would
// stand. This handler reads which of the two the check reported and decides
// nothing about severity.
//
// A stack the resolver refuses — the workspace configuration lists a directory in
// it that is not a directory, nothing existing there, say — is answered the same
// way: the check is not run, and the file carries the one finding the command
// line's check refuses with, at the error tier, naming the entry, the file and
// line listing it, and the setting it sits under. Checking the entity anyway
// would check it against fewer catalogs than the configuration lists. A clean
// check reports nothing. A request withdrawn while the check runs reports nothing
// either, and the caller leaves what the file last showed in place.
func (s *Server) runEntityValidation(ctx context.Context, releasePath, file string, content []byte) []Diagnostic {
	if _, err := s.resolvedWorkspace.ReleaseReach(releasePath, nil); err != nil {
		return []Diagnostic{s.entityCheckFinding(file, content, err, editorSeverityError)}
	}
	dirs, err := s.catalogDirsFor(releasePath)
	if err != nil {
		return []Diagnostic{s.entityCheckFinding(file, content, err, editorSeverityError)}
	}
	named, err := s.namedCatalogDirsFor(releasePath)
	if err != nil {
		return []Diagnostic{s.entityCheckFinding(file, content, err, editorSeverityError)}
	}
	res, err := validateops.ValidateEntityContent(ctx, s.workspaceRoot, s.wsConfig.ResolvedArtifactCacheDir(),
		dirs, named, file, content, false, validateops.WithDisplay(s.runDisplay()))
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	var diags []Diagnostic
	for _, note := range res.Notes {
		diags = append(diags, closureAdvisoryDiagnostic(strings.TrimSpace(note)))
	}
	for _, warning := range res.Warnings {
		diags = append(diags, s.entityCheckFinding(file, content, warning, editorSeverityWarning))
	}
	if err != nil {
		diags = append(diags, s.entityCheckFinding(file, content, err, editorSeverityError))
	}
	return diags
}

// entityCheckFinding maps one finding a single-entity check of file reports onto
// file's diagnostics, at severity, in the check's own words and with the
// identifiers its chain names. The severity is the one the check reported the
// finding at — the error tier for the finding it fails on, the warning tier for a
// finding it reports as a warning — and is never decided here.
//
// It is drawn where its own chain points within file, read through
// compilerops.FindingLocation — the one reading of where a finding points, which a
// refusal raised while rendering a Release is placed through too — so this check
// and a build of a Release deploying the entity place one finding at one key. A
// Recipe assertion that calls fail is drawn on that assertion's own key, which the
// check states for the Recipe it was handed; a finding whose chain names a key in
// the entity is drawn on that key.
//
// A finding whose chain names another document, or this one and no key, is still
// this file's finding — the check is about this entity — and is drawn where
// keyPathSpan draws every finding it cannot place at a key, and recorded as
// unplaced. The path the chain named is published only where it is a path into
// this file, since the entry's key path is a coordinate within the document the
// entry stands on.
func (s *Server) entityCheckFinding(file string, content []byte, err error, severity string) Diagnostic {
	var keyPath string
	if document, key := compilerops.FindingLocation(err, s.workspaceRoot); document != "" &&
		filepath.Clean(document) == filepath.Clean(file) {
		keyPath = key
	}
	doc, idx := s.advisoryPositionSource(content)
	line, col, endLine, endCol, located := keyPathSpan(doc, idx, keyPath)
	return Diagnostic{
		Line:           line,
		Column:         col,
		EndLine:        endLine,
		EndColumn:      endCol,
		Severity:       severity,
		Message:        err.Error(),
		ConditionCodes: conditionCodesOf(err),
		KeyPath:        keyPath,
		unplaced:       !located,
	}
}
