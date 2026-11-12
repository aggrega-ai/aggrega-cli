// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// hostBearingLibraryRefInComment matches a rejected host-bearing cross-document
// library reference: the SchemaLibrary $ref form
// (oci://<host>/<library>@<version>#/schemas/<entry>) and the MappingLibrary
// $import form (oci://…#/mappings/<entry>). The compiler rejects host-bearing
// references on both surfaces; the authored form is the bare-name identity
// <Library>#/schemas/<entry> or <Library>#/mappings/<entry>. A godoc comment in
// this package teaching either host-bearing form contradicts the compiler.
//
// The character class stops at spaces, double-quotes, and backticks so a
// truncated rejection mention does not trip it.
var hostBearingLibraryRefInComment = regexp.MustCompile("oci://[^ \"`]*#/(schemas|mappings)/")

// TestNoHostBearingLibraryRefInGodoc is the recurrence guard for the
// identity-reference doc model on this package's godoc. It scans comment lines
// only: string literals in test fixtures legitimately carry the host-bearing
// form to assert its rejection, and are not teaching surfaces. The guard is
// scoped to pkg/types (the authoring-type godoc home) by construction — the
// rejection detector (pkg/compactform) and the closure-walk discovery
// (pkg/closure) legitimately name the form in non-teaching comments in other
// packages.
func TestNoHostBearingLibraryRefInGodoc(t *testing.T) {
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
			if got := hostBearingLibraryRefInComment.FindString(line); got != "" {
				t.Errorf("%s:%d godoc teaches a rejected host-bearing library reference %q; use the bare-name identity form <Library>#/schemas/<entry> or <Library>#/mappings/<entry>", name, i+1, got)
			}
		}
	}
}

// TestHostBearingLibraryRefCommentMatcherIsNonVacuous proves the matcher fires
// on both rejected surfaces so the guard above is not vacuously green.
func TestHostBearingLibraryRefCommentMatcherIsNonVacuous(t *testing.T) {
	t.Parallel()
	planted := []string{
		"// reference via {$ref: oci://hub.aggrega.ai/x/DbSizing@1.0.0#/schemas/Storage}",
		"// reference via {$import: oci://hub.aggrega.ai/x/BaseMappings@1.0.0#/mappings/SimpleValues}",
	}
	for _, p := range planted {
		if !hostBearingLibraryRefInComment.MatchString(p) {
			t.Fatalf("matcher failed to catch a planted host-bearing comment %q; the guard is vacuous", p)
		}
	}
}
