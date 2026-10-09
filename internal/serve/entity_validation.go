// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"errors"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

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
// the other two surfaces refuse it with, on this file, rather than refused: this
// handler reports a Release it cannot read as something the author can fix, and
// checking the entity anyway would check it in the workspace's terms while
// answering as though in the Release's.
//
// The check reports one blocking finding at most, at the head of the file, in the
// check's own words — the same text the tool reports — and the advisory lines it
// gathers while reading the catalog, which never fail its verdict, at the warning
// tier ahead of it, the order they were decided in. A configured catalog directory
// where nothing exists is not in the stack the check is handed, so it is read as
// holding nothing, as the command line's check reads it. A clean check reports nothing. A request
// withdrawn while the check runs reports nothing either, and the caller leaves
// what the file last showed in place.
func (s *Server) runEntityValidation(ctx context.Context, releasePath, file string, content []byte) []Diagnostic {
	if _, err := s.resolvedWorkspace.ReleaseReach(releasePath, nil); err != nil {
		return []Diagnostic{{
			Line:           1,
			Column:         1,
			Severity:       editorSeverityError,
			Message:        err.Error(),
			ConditionCodes: conditionCodesOf(err),
		}}
	}
	res, err := validateops.ValidateEntityContent(ctx, s.workspaceRoot, s.wsConfig.ResolvedArtifactCacheDir(),
		s.catalogDirsFor(releasePath), s.namedCatalogDirsFor(releasePath), file, content, false)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	var diags []Diagnostic
	for _, note := range res.Notes {
		diags = append(diags, closureAdvisoryDiagnostic(strings.TrimSpace(note)))
	}
	if err != nil {
		diags = append(diags, Diagnostic{
			Line:           1,
			Column:         1,
			Severity:       editorSeverityError,
			Message:        err.Error(),
			ConditionCodes: conditionCodesOf(err),
		})
	}
	return diags
}
