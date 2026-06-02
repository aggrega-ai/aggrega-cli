// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// hostBearingSchemaRefInComment matches the rejected host-bearing SchemaLibrary
// cross-document $ref form (oci://<host>/<library>@<version>#/schemas/<entry>).
// The compiler rejects host-bearing $ref references; the authored form is the
// bare-name identity <Library>#/schemas/<entry>. A godoc comment in this package
// teaching the host-bearing form contradicts the compiler.
//
// Scope is the SchemaLibrary $ref surface (#/schemas/) only. The MappingLibrary
// $import surface (#/mappings/) still recognises a library import by the oci://
// prefix (the renderer's HasOCIPrefix), so its host-bearing form is the live
// discrimination form — extending the guard to #/mappings/ awaits the $import
// reconciliation. The character class stops at spaces, double-quotes, and
// backticks so a truncated rejection mention does not trip it.
var hostBearingSchemaRefInComment = regexp.MustCompile("oci://[^ \"`]*#/schemas/")

// TestNoHostBearingSchemaRefInGodoc is the recurrence guard for the
// identity-reference doc model on this package's godoc. It scans comment lines
// only: string literals in test fixtures legitimately carry the host-bearing
// form to assert its rejection, and are not teaching surfaces. The guard is
// scoped to pkg/types (the authoring-type godoc home) by construction — the
// rejection detector (pkg/compactform) and the closure-walk discovery
// (pkg/closure) legitimately name the form in non-teaching comments in other
// packages.
func TestNoHostBearingSchemaRefInGodoc(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if got := hostBearingSchemaRefInComment.FindString(line); got != "" {
				t.Errorf("%s:%d godoc teaches the rejected host-bearing SchemaLibrary $ref form %q; use the bare-name identity form <Library>#/schemas/<entry>", name, i+1, got)
			}
		}
	}
}

// TestHostBearingSchemaRefCommentMatcherIsNonVacuous proves the matcher fires on
// the rejected form so the guard above is not vacuously green.
func TestHostBearingSchemaRefCommentMatcherIsNonVacuous(t *testing.T) {
	t.Parallel()
	const planted = "// reference via {$ref: oci://hub.aggrega.ai/x/DbSizing@1.0.0#/schemas/Storage}"
	if !hostBearingSchemaRefInComment.MatchString(planted) {
		t.Fatalf("matcher failed to catch a planted host-bearing comment %q; the guard is vacuous", planted)
	}
}
