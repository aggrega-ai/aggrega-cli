// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// licenceHeader is the comment a catalog file opens with, so the head of every
// document below is a comment rather than a key.
const licenceHeader = "# SPDX-License-Identifier: Apache-2.0\n# Copyright 2026 Cognitio S.r.l.\n\n"

// TestKeyPathSpanDrawsAFindingItCannotPlaceOnTheKeyNamingTheDocument holds the one
// fall-back every finding positioned from a path takes: a path that resolves is
// drawn on its key; otherwise the entry is drawn on the document's kind: key, on
// its metadata.name where it declares no kind:, and at the head of the file only
// where it declares neither or did not parse. resolved is true only for the
// first, so a surface stating the absence of a position still can.
//
// Each expected position is read off the row's own text — the line the key is
// written on — so the table cannot agree with the code under test by construction.
func TestKeyPathSpanDrawsAFindingItCannotPlaceOnTheKeyNamingTheDocument(t *testing.T) {
	t.Parallel()
	full := licenceHeader + "apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: broker\n" +
		"spec:\n  assertions:\n    ready: 'ok'\n"
	noKind := licenceHeader + "apiVersion: aggrega.ai/v1alpha1\nmetadata:\n  name: broker\nspec: {}\n"
	// metadata is declared and carries no name, so neither anchor is written as
	// such: the metadata key is not what the fall-back names.
	neither := licenceHeader + "apiVersion: aggrega.ai/v1alpha1\nmetadata:\n  version: \"1.0.0\"\nspec: {}\n"

	for _, row := range []struct {
		name    string
		content string
		keyPath string
		// at is the key the entry must be drawn on, as written at the head of its
		// line, or "" for the head of the file.
		at       string
		resolved bool
		// unparseable passes no document, as a surface holding bytes no parser reads
		// does.
		unparseable bool
	}{
		{name: "a path that resolves is drawn on its key", content: full,
			keyPath: ".spec.assertions.ready", at: "ready:", resolved: true},
		{name: "no path is drawn on kind:", content: full, at: "kind:"},
		{name: "a path into a different document is drawn on kind:", content: full,
			keyPath: ".boundaries.app.components", at: "kind:"},
		{name: "a document with no kind: is drawn on metadata.name", content: noKind, at: "name:"},
		{name: "a document with neither is drawn at the head of the file", content: neither},
		{name: "a document that did not parse is drawn at the head of the file", content: full,
			keyPath: ".spec.assertions.ready", unparseable: true},
	} {
		row := row
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			var doc *yaml.Node
			idx := yamlpos.LineIndex{}
			if !row.unparseable {
				doc = parseYAML(t, row.content)
				idx = lineIndexFor(row.content)
			}
			line, col, endLine, endCol, resolved := keyPathSpan(doc, idx, row.keyPath)

			wantLine, wantCol, wantEndLine, wantEndCol := 1, 1, 0, 0
			if row.at != "" {
				wantLine, wantCol = keyPosition(t, row.content, row.at)
				wantEndLine, wantEndCol = wantLine, wantCol+len(strings.TrimSuffix(row.at, ":"))
			}
			if line != wantLine || col != wantCol || endLine != wantEndLine || endCol != wantEndCol {
				t.Errorf("drawn at %d:%d-%d:%d; want %d:%d-%d:%d", line, col, endLine, endCol,
					wantLine, wantCol, wantEndLine, wantEndCol)
			}
			if resolved != row.resolved {
				t.Errorf("resolved = %v; want %v — only a path that walked to a key is resolved", resolved, row.resolved)
			}
		})
	}
}

// TestAFindingAtTheFallBackRestatesARefusalWhereverTheFallBackIsDrawn holds the
// restatement rule to what a finding's position stands for rather than to the
// position itself. A finding drawn on its document's kind: key because nothing it
// carried resolved is placed no more precisely than its document, as one drawn at
// the head of the file is, so it restates a build refusal of the same condition
// wherever that refusal sits; a finding a producer genuinely placed on that same
// key is a finding at a key, and restates only a refusal at that key.
func TestAFindingAtTheFallBackRestatesARefusalWhereverTheFallBackIsDrawn(t *testing.T) {
	t.Parallel()
	codes := conditionCodesOf(renderer.ErrAssertionFailed())
	if codes.Code == "" {
		t.Fatal("the condition carries no code, so every row would restate nothing")
	}
	refusal := Diagnostic{Line: 23, Column: 5, EndLine: 23, EndColumn: 35, ConditionCodes: codes}
	atKind := func(unplaced bool) Diagnostic {
		return Diagnostic{Line: 5, Column: 1, EndLine: 5, EndColumn: 5, ConditionCodes: codes, unplaced: unplaced}
	}
	for _, row := range []struct {
		name    string
		finding Diagnostic
		want    bool
	}{
		{name: "a finding drawn at the fall-back restates a refusal at a key", finding: atKind(true), want: true},
		{name: "a finding placed on the kind: key restates no refusal elsewhere", finding: atKind(false)},
		{name: "a finding a producer draws at the head of the file restates a refusal at a key",
			finding: Diagnostic{Line: 1, Column: 1, ConditionCodes: codes}, want: true},
		{name: "a finding at the refusal's own span restates it", finding: refusal, want: true},
		{name: "a finding naming another condition restates nothing",
			finding: Diagnostic{Line: 5, Column: 1, EndLine: 5, EndColumn: 5, unplaced: true,
				ConditionCodes: conditionCodesOf(renderer.ErrTemplateUnknownRoot())}},
	} {
		if got := restatesRefusal(row.finding, refusal); got != row.want {
			t.Errorf("%s: restatesRefusal = %v; want %v", row.name, got, row.want)
		}
	}
}
