// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// The Recipe whose artifacts block holds an entry whose key is a filename.
//
// It is a real authoring shape rather than a contrived one: an artifacts key is
// an output filename, so a dot in it is the ordinary case and not an edge one.
const locatorReadingRecipe = `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: example/app
spec:
  artifacts:
    policy.yaml: |
      rule: deny
`

// TestALocatorHasExactlyOneReading is the injectivity assertion: two different
// segment lists cannot arrive at one locator.
//
// The pair is the one the dialect makes possible. An artifact's output name is a
// filename, so "policy.yaml" is ONE authored key; a path descending through a key
// "policy" to a key "yaml" is TWO. Joined with the separator both would read
// ".spec.artifacts.policy.yaml", and a reader handed that string — a person, or a
// walk recovering a position — has no way to tell which of the two it names.
//
// INVARIANT: splitting a locator on the separator yields exactly the segments it
// was assembled from.
// CONSEQUENCE: without it, a position is a guess. The author is pointed at a key
// the document does not contain, and cannot tell whether the fault is in the
// diagnostic or in the file.
func TestALocatorHasExactlyOneReading(t *testing.T) {
	t.Parallel()

	oneKey := documentSpecSegments(authoredKeyArtifacts, "policy.yaml")
	twoKeys := documentSpecSegments(authoredKeyArtifacts, "policy", "yaml")

	fromOneKey := authoredLocator(oneKey...)
	fromTwoKeys := authoredLocator(twoKeys...)

	if fromOneKey == fromTwoKeys {
		t.Fatalf("the segments %q and %q both produced %q, so one locator serves two readings",
			oneKey, twoKeys, fromOneKey)
	}

	// Stated as its own assertion rather than left to the inequality above: a
	// change producing two DIFFERENT wrong answers would satisfy that inequality
	// while naming nothing either document contains.
	if want := ".spec.artifacts.policy.yaml"; fromTwoKeys != want {
		t.Errorf("a path through two keys = %q; want %q — the reading that names a real "+
			"nesting is the one the dialect keeps", fromTwoKeys, want)
	}
	if want := ".spec.artifacts"; fromOneKey != want {
		t.Errorf("a single key carrying the separator = %q; want %q — the deepest site the "+
			"document can be walked to", fromOneKey, want)
	}

	// Every segment of what is emitted survives a round trip through the dialect,
	// which is the mechanical form of the invariant.
	for _, locator := range []string{fromOneKey, fromTwoKeys} {
		for _, segment := range strings.Split(strings.TrimPrefix(locator, "."), ".") {
			if strings.Contains(segment, locatorSeparator) {
				t.Errorf("the locator %q carries a segment %q holding the separator, so splitting "+
					"it does not recover what it was built from", locator, segment)
			}
		}
	}
}

// TestTruncatingAnUnaddressableKeyMovesNoPosition is the other half of the trade,
// and it is measured against the real position walk rather than argued.
//
// Stopping at the last addressable segment costs depth, so the question is what
// that costs a reader. It costs nothing: the walk that recovers a position from a
// locator descends by literal keys, so it already stops at exactly that segment
// when the next one is not a key the document holds. The long form and the short
// form therefore land a reader on the same key, and the only thing that changes
// is that the string stops asserting a path the file does not contain.
//
// It goes through [yamlpos.FieldNode] — the walk every surface turning a finding
// into a source position resolves through — rather than through a walk written
// here. A local walk would be a second copy of that fallback rule, and the two
// agreeing today is exactly what would hide a later divergence.
func TestTruncatingAnUnaddressableKeyMovesNoPosition(t *testing.T) {
	t.Parallel()

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(locatorReadingRecipe), &doc); err != nil {
		t.Fatalf("the authored Recipe does not parse: %v", err)
	}

	// The form the dialect would have produced by joining the filename in, and the
	// form it produces instead.
	joined := ".spec.artifacts.policy.yaml"
	emitted := authoredLocator(documentSpecSegments(authoredKeyArtifacts, "policy.yaml")...)

	fromJoined := yamlpos.FieldNode(&doc, joined)
	fromEmitted := yamlpos.FieldNode(&doc, emitted)
	if fromJoined == nil || fromEmitted == nil {
		t.Fatalf("one of the two forms resolved to nothing (%q -> %v, %q -> %v), so the "+
			"comparison below has no operands", joined, fromJoined, emitted, fromEmitted)
	}
	if fromJoined.Line != fromEmitted.Line || fromJoined.Column != fromEmitted.Column {
		t.Errorf("the joined form positioned at %d:%d and the emitted form at %d:%d; the two are "+
			"expected to coincide, and a change separating them changes what the shorter form costs "+
			"a reader", fromJoined.Line, fromJoined.Column, fromEmitted.Line, fromEmitted.Column)
	}

	// The floor the comparison is measured against. Without it a reader could take
	// the agreement for a deliberate coarsening rather than for the walk having
	// stopped there all along.
	if fromEmitted.Value != authoredKeyArtifacts {
		t.Errorf("the emitted form reached key %q; want %q — the container that holds the entry",
			fromEmitted.Value, authoredKeyArtifacts)
	}
}

// TestALeafPathKeepsItsDepth pins that the two values reaching locator assembly
// are told apart.
//
// A configuration leaf names a PATH into its pillar and an artifact name is ONE
// key, and both arrive as a single string. Telling them apart is what lets the
// first keep its depth while the second stops at its container — so this is the
// assertion that the fix scoped itself to what it measured instead of coarsening
// every axis to reach one.
func TestALeafPathKeepsItsDepth(t *testing.T) {
	t.Parallel()

	segments := authoredSiteSegments(
		subsystemNesting,
		AuthoredSite{Component: "web"},
		append(
			[]string{authoredKeyFeatures, "audit", authoredKeyValues},
			pillarLeafSegments("retention.days")...,
		)...,
	)
	if want := ".spec.components.web.features.audit.values.retention.days"; authoredLocator(segments...) != want {
		t.Errorf("a leaf path produced %q; want %q — its levels are keys the document spells, "+
			"one under the next", authoredLocator(segments...), want)
	}

	// A raise positioned inside a subject takes its leaf in the same dialect, so
	// the same distinction has to hold there.
	p := newProvenance("aggregate.yaml", authoredKeySpec, authoredKeyComponents, "web")
	at := p.RaiseAt(errRuleBroken, "values.replicas", "minimum")
	if want := ".spec.components.web.values.replicas"; at.Locator() != want {
		t.Errorf("a raise at a leaf path produced %q; want %q", at.Locator(), want)
	}
	if got := at.Detail(); len(got) == 0 || got[0] != "values.replicas" {
		t.Errorf("the detail's leading value = %v; want the leaf whole — the detail names the "+
			"value the rule reported, and only the locator is a position", got)
	}
}
