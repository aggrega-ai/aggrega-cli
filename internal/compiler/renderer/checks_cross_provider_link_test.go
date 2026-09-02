// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// The rule over the two providers a connection's ends render through reads four
// declared facts — the node at the far end, whether that node is in the flattened
// set, each end's rendering provider, and each end's cross-boundary-linking claim
// — and this file runs the declared value through the framework's own traversal.
//
// # Why the rows are driven through the traversal rather than the predicate
//
// A call straight into the predicate would prove the comparison and nothing about
// the axis. The rule is invoked once per connection, and it reads the far end from
// a fact rather than from the coordinate it was invoked with — so a traversal over
// the wrong set, or a predicate that mistook the alias for the target, would leave
// every refusal row quiet and report exactly what a rule that ran and approved
// reports. Driving the traversal puts both under test.
//
// # The stub embeds the view interface, and the nil embedding is the point
//
// crossProviderLinkFactStub embeds [constraint.DescribedView] and overrides only
// the four accessors this rule reads. The embedded interface is nil, so any other
// accessor panics rather than answering a zero value — which turns "the predicate
// reads declared facts and nothing else" from a claim about the source into a
// property this file enforces.
//
// # The equivalence cross-check, and when it should go
//
// TestCrossProviderLinkRuleAgreesWithTheClassifier compares the declared rule's
// FINDINGS against the classifier the link pass and the placeholder stamp both
// still read, over one matrix of inputs. It is a migration proof with a stated
// lifetime, not a permanent twin: it exists because the finding moved off the
// pass, and it should be removed once the two other callers of
// [classifyCrossBoundaryLink] are themselves declared rules — at which point the
// classifier has no separate reading left to agree with and the comparison
// compares the rule to itself. Retained cross-checks of this shape are being
// retired, so this one carries that condition rather than an open end.

// crossProviderLinkFactStub answers the four facts the rule reads and nothing
// else.
type crossProviderLinkFactStub struct {
	constraint.DescribedView

	// targets is the node each connection resolves to, keyed by the edge triple
	// joined with a separator no node id, socket or alias contains.
	targets map[string]string

	// inSet is the flattened node set: membership, which is what keeps the rule
	// silent about a connection whose far end is not a component.
	inSet map[string]bool

	// providers is each node's rendering provider. A node present in the set with
	// no entry here renders through no provider, which is the empty answer the
	// fact gives for a node with no bound Recipe.
	providers map[string]string

	// links is each node's provider's cross-boundary-linking claim.
	links map[string]bool
}

// edgeKey joins an edge's triple into the stub's map key. The separator is a
// character no node id, socket or alias can carry, so two distinct triples cannot
// collide onto one entry.
func edgeKey(node, socket, alias string) string {
	return node + "\x00" + socket + "\x00" + alias
}

func (s crossProviderLinkFactStub) PerEdgeResolvedTarget(node, socket, alias string) string {
	return s.targets[edgeKey(node, socket, alias)]
}

func (s crossProviderLinkFactStub) FlattenedNodeComponentType(node string) (string, bool) {
	if !s.inSet[node] {
		return "", false
	}
	// The type itself is not read by this rule; membership is. A non-empty value
	// is returned anyway so a row cannot pass by the two halves agreeing.
	return "core/compute/any", true
}

func (s crossProviderLinkFactStub) NodeRenderingProvider(node string) string {
	return s.providers[node]
}

func (s crossProviderLinkFactStub) ProviderCrossBoundaryLinking(node string) bool {
	return s.links[node]
}

// crossProviderLinkEdges is the connection axis: the set the rule is invoked once
// per, in the order it is returned.
type crossProviderLinkEdges struct {
	edges []constraint.EdgeInstance
}

func (e crossProviderLinkEdges) Edges() []constraint.EdgeInstance { return e.edges }

// oneConnection builds an axis entry for a connection authored on one node.
func oneConnection(node, socket, alias string) constraint.EdgeInstance {
	return constraint.EdgeInstance{
		Node:   constraint.NodeInstance{ID: node, SourceFile: "aggregate.yaml", AuthoredName: node},
		Socket: socket,
		Alias:  alias,
	}
}

// crossProviderLinkAdvisories drives the declared rule over an axis and a fact
// stub through the framework's own traversal, and returns the warnings it raised.
//
// It is the shape the build takes rather than a shortcut into it: the same
// traversal, the same advisory conversion, and the same stream the render appends
// to.
func crossProviderLinkAdvisories(
	t *testing.T,
	edges []constraint.EdgeInstance,
	view crossProviderLinkFactStub,
) []string {
	t.Helper()
	raised, err := runCrossBoundaryLinkProviderRule(
		context.Background(), view, crossProviderLinkEdges{edges: edges},
	)
	if err != nil {
		t.Fatalf("the advisory walk returned an error: %v", err)
	}
	// Rendered through the one display formatter, because the rows below assert
	// on the sentence a reader is shown.
	out := make([]string, 0, len(raised))
	for _, d := range raised {
		out = append(out, RenderDiagnosticText(d))
	}
	return out
}

// twoEndFixture builds the ordinary shape these rows vary: one connection from
// "consumer" to "producer", with each end's provider and linking claim supplied.
func twoEndFixture(
	consumerProvider, targetProvider string, consumerLinks, targetLinks bool,
) ([]constraint.EdgeInstance, crossProviderLinkFactStub) {
	edges := []constraint.EdgeInstance{oneConnection("consumer", "db", "primary")}
	view := crossProviderLinkFactStub{
		targets:   map[string]string{edgeKey("consumer", "db", "primary"): "producer"},
		inSet:     map[string]bool{"consumer": true, "producer": true},
		providers: map[string]string{"consumer": consumerProvider, "producer": targetProvider},
		links:     map[string]bool{"consumer": consumerLinks, "producer": targetLinks},
	}
	return edges, view
}

// TestCrossProviderLinkRule_Refuses is the REFUSAL corpus: every shape in which a
// connection spans two providers with at least one of them advertising linking.
func TestCrossProviderLinkRule_Refuses(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name                             string
		consumerProvider, targetProvider string
		consumerLinks, targetLinks       bool
	}{
		{"the consumer advertises linking", "cloudformation", "helm", true, false},
		{"the target advertises linking", "helm", "cloudformation", false, true},
		{"both advertise linking", "cloudformation", "terraform", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			edges, view := twoEndFixture(
				tc.consumerProvider, tc.targetProvider, tc.consumerLinks, tc.targetLinks)
			got := crossProviderLinkAdvisories(t, edges, view)
			if len(got) != 1 {
				t.Fatalf("want exactly one advisory, got %d: %v", len(got), got)
			}
			// The advisory carries the rule's registered wording, its position, and
			// every coordinate an operator needs to find the connection.
			for _, want := range []string{
				ErrCrossProviderLinkUnsupported().Error(),
				"aggregate.yaml",
				".spec.components.consumer.uses.db.primary",
				`"producer"`,
				`socket "db"`,
				`consumer provider "` + tc.consumerProvider + `"`,
				`target provider "` + tc.targetProvider + `"`,
			} {
				if !strings.Contains(got[0], want) {
					t.Errorf("advisory does not carry %q:\n%s", want, got[0])
				}
			}
		})
	}
}

// TestCrossProviderLinkRule_RefusesATargetWithNoProvider is the refusal row for a
// far end that IS a component and binds no Recipe.
//
// It renders through no provider, which differs from whatever its peer binds, so a
// linking consumer has nothing to link to and the author is told. It is the row
// that separates the membership read from the provider read: silencing this case
// by treating an empty provider as "no answer" would drop a finding the shipped
// reading raised.
func TestCrossProviderLinkRule_RefusesATargetWithNoProvider(t *testing.T) {
	t.Parallel()
	edges, view := twoEndFixture("cloudformation", "", true, false)
	got := crossProviderLinkAdvisories(t, edges, view)
	if len(got) != 1 {
		t.Fatalf("want exactly one advisory, got %d: %v", len(got), got)
	}
}

// TestCrossProviderLinkRule_ReportsEveryOffendingConnection is the cardinality
// row: two connections across the same seam raise two advisories, each positioned
// at its own authored site.
//
// A rule folding them into one finding, or stopping at the first, would satisfy
// every row above and leave an author correcting one of two.
func TestCrossProviderLinkRule_ReportsEveryOffendingConnection(t *testing.T) {
	t.Parallel()

	edges := []constraint.EdgeInstance{
		oneConnection("consumer", "db", "primary"),
		oneConnection("consumer", "cache", "hot"),
	}
	view := crossProviderLinkFactStub{
		targets: map[string]string{
			edgeKey("consumer", "db", "primary"): "producer",
			edgeKey("consumer", "cache", "hot"):  "sidecar",
		},
		inSet: map[string]bool{"consumer": true, "producer": true, "sidecar": true},
		providers: map[string]string{
			"consumer": "cloudformation", "producer": "helm", "sidecar": "terraform",
		},
		links: map[string]bool{"consumer": true},
	}

	got := crossProviderLinkAdvisories(t, edges, view)
	if len(got) != 2 {
		t.Fatalf("want one advisory per offending connection, got %d: %v", len(got), got)
	}
	// The order is the axis's own, so an operator reading two builds of identical
	// input reads the same two lines in the same sequence.
	if !strings.Contains(got[0], ".uses.db.primary") {
		t.Errorf("first advisory is not the first connection the axis yielded:\n%s", got[0])
	}
	if !strings.Contains(got[1], ".uses.cache.hot") {
		t.Errorf("second advisory is not the second connection the axis yielded:\n%s", got[1])
	}
}

// TestCrossProviderLinkRule_Silent is the SILENT corpus: every shape the rule must
// pass over.
//
// It is the half a refusal-only corpus cannot see. A rule that reported every
// connection would satisfy each refusal row above and fail every row here.
func TestCrossProviderLinkRule_Silent(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		edges []constraint.EdgeInstance
		view  crossProviderLinkFactStub
	}{
		{
			name: "one provider, both ends advertise linking — the crossing is wired natively",
			edges: func() []constraint.EdgeInstance {
				e, _ := twoEndFixture("cloudformation", "cloudformation", true, true)
				return e
			}(),
			view: func() crossProviderLinkFactStub {
				_, v := twoEndFixture("cloudformation", "cloudformation", true, true)
				return v
			}(),
		},
		{
			name: "one provider, neither end advertises linking",
			edges: func() []constraint.EdgeInstance {
				e, _ := twoEndFixture("helm", "helm", false, false)
				return e
			}(),
			view: func() crossProviderLinkFactStub {
				_, v := twoEndFixture("helm", "helm", false, false)
				return v
			}(),
		},
		{
			name: "two providers, neither advertises linking — nothing was going to be wired",
			edges: func() []constraint.EdgeInstance {
				e, _ := twoEndFixture("helm", "terraform", false, false)
				return e
			}(),
			view: func() crossProviderLinkFactStub {
				_, v := twoEndFixture("helm", "terraform", false, false)
				return v
			}(),
		},
		{
			name:  "the far end is not a component in the flattened set",
			edges: []constraint.EdgeInstance{oneConnection("consumer", "db", "primary")},
			view: crossProviderLinkFactStub{
				targets:   map[string]string{edgeKey("consumer", "db", "primary"): "absent"},
				inSet:     map[string]bool{"consumer": true},
				providers: map[string]string{"consumer": "cloudformation"},
				links:     map[string]bool{"consumer": true},
			},
		},
		{
			name:  "the coordinate addresses no connection, so no far end resolves",
			edges: []constraint.EdgeInstance{oneConnection("consumer", "db", "primary")},
			view: crossProviderLinkFactStub{
				targets:   map[string]string{},
				inSet:     map[string]bool{"consumer": true, "producer": true},
				providers: map[string]string{"consumer": "cloudformation", "producer": "helm"},
				links:     map[string]bool{"consumer": true},
			},
		},
		{
			name:  "no connections at all",
			edges: nil,
			view:  crossProviderLinkFactStub{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := crossProviderLinkAdvisories(t, tc.edges, tc.view); len(got) != 0 {
				t.Fatalf("want no advisory, got %d: %v", len(got), got)
			}
		})
	}
}

// TestCrossProviderLinkRuleReadsTheFarEndFromTheFactNotTheAlias pins the axis
// property the rule rests on: the far end is a fact keyed by the connection, and
// never the alias the connection carries.
//
// The two coincide in the short authoring form, where a connection's alias IS its
// target's name — which is exactly what makes a predicate that read the alias pass
// every other row in this file. Here the alias and the target deliberately differ,
// and the providers are arranged so that reading the alias would find no provider
// pair to object about.
func TestCrossProviderLinkRuleReadsTheFarEndFromTheFactNotTheAlias(t *testing.T) {
	t.Parallel()

	edges := []constraint.EdgeInstance{oneConnection("consumer", "db", "primary")}
	view := crossProviderLinkFactStub{
		targets: map[string]string{edgeKey("consumer", "db", "primary"): "producer"},
		// "primary" is in the set and shares the consumer's provider, so a rule
		// reading the alias as the far end finds one provider and stays silent.
		inSet: map[string]bool{"consumer": true, "producer": true, "primary": true},
		providers: map[string]string{
			"consumer": "cloudformation", "producer": "helm", "primary": "cloudformation",
		},
		links: map[string]bool{"consumer": true},
	}

	got := crossProviderLinkAdvisories(t, edges, view)
	if len(got) != 1 {
		t.Fatalf("the rule must read the far end from the fact keyed by the connection; got %d: %v",
			len(got), got)
	}
	if !strings.Contains(got[0], `"producer"`) {
		t.Errorf("the advisory names the wrong far end:\n%s", got[0])
	}
}

// TestCrossProviderLinkRuleAgreesWithTheClassifier is the equivalence cross-check
// clause of this migration: over one matrix of inputs, the declared rule raises a
// finding exactly when the classifier the link pass and the placeholder stamp both
// read rules the connection a cross-provider crossing.
//
// It compares FINDINGS and not message bytes. The two readings compose their text
// differently by construction — the classifier composes none at all, it returns a
// class — so a byte comparison would be a comparison of two things only one of
// which exists.
//
// # The matrix varies the assembly path, and that is the point
//
// The classifier is keyed by each end's whole dispatch key, path included; the
// declared rule reads no path at all. The matrix therefore holds rows whose paths
// agree and rows whose paths differ, which is what turns "the path cannot change
// the verdict" from an argument into a measurement: a group is keyed by a path AND
// a provider together, so two differing providers are in different groups whatever
// their paths, and two matching providers are never the case this rule is about.
//
// # Its lifetime
//
// Remove it once the classifier's two remaining callers are themselves declared
// rules. At that point there is no second reading for the rule to agree with, and
// this test compares the rule to itself.
func TestCrossProviderLinkRuleAgreesWithTheClassifier(t *testing.T) {
	t.Parallel()

	paths := []struct {
		name             string
		srcPath, tgtPath string
	}{
		{"paths differ", "boundaries/app", "boundaries/data"},
		{"paths agree", "boundaries/shared", "boundaries/shared"},
	}
	providers := []struct {
		name                 string
		srcProvider, tgtProv string
	}{
		{"providers differ", "cloudformation", "helm"},
		{"providers agree", "helm", "helm"},
		{"the target binds no Recipe", "cloudformation", ""},
	}
	claims := []struct {
		name               string
		srcLinks, tgtLinks bool
	}{
		{"neither advertises", false, false},
		{"the consumer advertises", true, false},
		{"the target advertises", false, true},
		{"both advertise", true, true},
	}

	rows := 0
	for _, p := range paths {
		for _, pr := range providers {
			for _, c := range claims {
				name := p.name + ", " + pr.name + ", " + c.name
				t.Run(name, func(t *testing.T) {
					t.Parallel()

					edges, view := twoEndFixture(pr.srcProvider, pr.tgtProv, c.srcLinks, c.tgtLinks)
					declared := len(crossProviderLinkAdvisories(t, edges, view))

					classified := classifyCrossBoundaryLink(
						dispatchKey{Path: p.srcPath, Provider: pr.srcProvider},
						dispatchKey{Path: p.tgtPath, Provider: pr.tgtProv},
						c.srcLinks, c.tgtLinks,
					)
					wantFindings := 0
					if classified == crossBoundaryLinkCrossProvider {
						wantFindings = 1
					}
					if declared != wantFindings {
						t.Fatalf("declared rule raised %d finding(s); the classifier ruled %v, which wants %d",
							declared, classified, wantFindings)
					}
				})
				rows++
			}
		}
	}

	// The matrix is enumerated rather than asserted: a comparison over a corpus
	// that silently shrank would agree with the classifier about nothing.
	if want := len(paths) * len(providers) * len(claims); rows != want {
		t.Fatalf("the matrix walked %d rows, want %d", rows, want)
	}
	if rows == 0 {
		t.Fatal("the matrix is empty, so the comparison proves nothing")
	}
}
