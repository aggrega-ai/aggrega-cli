// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The harness the rules over an authored annotation are driven through.
//
// Every assertion over those rules goes through the real entry point rather than
// calling a predicate directly, and the reason is the one the framework's whole
// shape rests on: a check is a declared value whose placement, traversal and
// provenance are the framework's, so a test that called its predicate would prove
// the predicate decides correctly while saying nothing about whether the rule is
// ever invoked, invoked once per authored entry, or handed a subject that can name
// the site. Those are exactly the ways a declared rule ships and reports nothing.

// authoredAnnotationRefusal asks the declared rules over what an author wrote in
// an annotation, through the entry point the pipeline calls, and returns what
// they refused.
func authoredAnnotationRefusal(
	t *testing.T,
	manifest *types.Manifest,
	aggregate *types.Aggregate,
	release *types.Release,
	catalog CatalogLookup,
) error {
	t.Helper()
	return ValidateAuthoredAnnotations(nil, &ResolveResult{
		Manifest:      manifest,
		Aggregate:     aggregate,
		Release:       release,
		AggregatePath: "aggregate.yaml",
		ReleasePath:   "release.yaml",
	}, catalog)
}

// aggregateAnnotating builds an Aggregate declaring one component carrying the
// supplied annotation block, which is the Aggregate side's component-keyed
// authoring site.
func aggregateAnnotating(t *testing.T, annotations string) *types.Aggregate {
	t.Helper()
	return loadTestAggregate(t, `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server:1.0.0
      annotations:
`+indentAnnotationBlock(annotations, 8))
}

// releaseAnnotating builds a Release declaring one component inside one
// boundary carrying the supplied annotation block, which is the Release
// side's component-keyed authoring site.
func releaseAnnotating(t *testing.T, annotations string) *types.Release {
	t.Helper()
	return loadTestRelease(t, `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: test
  version: "1.0.0"
  environment: testing
spec:
  aggregate: test
  boundaries:
    prod:
      components:
        api:
          annotations:
`+indentAnnotationBlock(annotations, 12))
}

// indentAnnotationBlock re-indents an annotation block written at column zero so
// it sits under the key the document above places it at.
func indentAnnotationBlock(block string, spaces int) string {
	pad := strings.Repeat(" ", spaces)
	var out strings.Builder
	for _, line := range strings.Split(strings.TrimRight(block, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			out.WriteString("\n")
			continue
		}
		out.WriteString(pad)
		out.WriteString(line)
		out.WriteString("\n")
	}
	return out.String()
}

// TestAuthoredAnnotationRulesEnumerateEveryAuthoredSite is the non-zero
// enumeration half, and it is a separate assertion from every halt below because
// a newly minted subject kind has two ways to be silent: a rule nothing invokes,
// and a rule invoked over a traversal that enumerated nothing. A halt proves the
// first; only this proves the second, and on a LEGAL document, where no halt is
// available to prove anything.
//
// It reads the enumeration through the presentation the entry point builds, over
// a document carrying an entry at every site an annotation can be written at:
// scope-wide and component-keyed on the Release, and the same pair on the
// Aggregate. A count alone would not say which sites were reached, so the
// criterion is the site set and the count is derived from it.
func TestAuthoredAnnotationRulesEnumerateEveryAuthoredSite(t *testing.T) {
	t.Parallel()
	release := loadTestRelease(t, `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: test
  version: "1.0.0"
  environment: testing
spec:
  aggregate: test
  boundaries:
    prod:
      annotations:
        feature.myco/rewrite-host: "scope.example"
      components:
        api:
          annotations:
            feature.myco/rewrite-host: "component.example"
`)
	aggregate := loadTestAggregate(t, `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  subsystems:
    billing:
      annotations:
        feature.myco/rewrite-host: "subsystem.example"
      components:
        api:
          type: core/server:1.0.0
          annotations:
            feature.myco/rewrite-host: "instance.example"
`)
	view := newNormalizedView(
		types.Normalize(aggregate), release, CatalogLookup{}, nil)

	got := map[string]bool{}
	for _, entry := range view.annotations {
		got[entry.Document+"|"+entry.Site.ScopePath+"|"+entry.Component] = true
	}
	want := []string{
		"release|prod|",
		"release|prod|api",
		"aggregate|billing|",
		"aggregate|billing|api",
	}
	for _, site := range want {
		if !got[site] {
			t.Errorf("the traversal enumerated no subject at the authored site %q.\n"+
				"INVARIANT: a rule keyed at the authored site is invoked once per entry the author wrote, "+
				"at every site the two documents let one be written at.\n"+
				"CONSEQUENCE: a site nothing enumerates is a site no rule is ever asked about, and a rule "+
				"invoked zero times raises nothing — which every gate reads as a document that passed.\n"+
				"enumerated: %v", site, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("the traversal enumerated %d site(s), want %d; got %v", len(got), len(want), got)
	}
}
