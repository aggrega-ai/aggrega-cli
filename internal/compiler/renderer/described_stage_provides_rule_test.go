// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The proofs in this file cover the site that invokes the rule declared over the
// text a Recipe wrote for each of its provides keys: that the rule is genuinely
// reached, that it reports every offending entry rather than the first, that the
// walk observes the caller's cancellation signal per iteration, and that the
// enumeration the render supplies presents the keys the Recipe declares.

// The cancellation and counting fixture. Three components, each declaring two
// provides keys, so a walk stopped after the first component's turn is a
// different number from a walk that ran to the end.
const provNodeCount, provKeysPerNode = 3, 2

func provNodeIDs() []string { return []string{"node-a", "node-b", "node-c"} }
func provKeyIDs() []string  { return []string{"first", "second"} }

// provAxes presents a node set and the provides keys each node's Recipe declares,
// which is the whole of what the rule is invoked once per.
type provAxes struct {
	nodes []string
	keys  []string
	file  string
}

func (a provAxes) Nodes() []constraint.NodeInstance {
	out := make([]constraint.NodeInstance, 0, len(a.nodes))
	for _, id := range a.nodes {
		out = append(out, constraint.NodeInstance{ID: id, SourceFile: "aggregate.yaml", AuthoredName: id})
	}
	return out
}

// Attachments answers empty: these proofs present no attachment, so the axis pair
// is satisfied without standing in for a cascade none of them exercises.
func (a provAxes) Attachments(_ string) []string { return nil }

// Edges answers empty for the reason Attachments does: these proofs present no
// connection, so the rule over a connection's two providers is invoked for
// nothing here and the rows below measure the provides walk alone. The method is
// present because the coordinate set names every axis the stage's rules are keyed
// by, which is what stops a fixture from silently omitting one.
func (a provAxes) Edges() []constraint.EdgeInstance { return nil }

func (a provAxes) ProvidesKeys(_ string) []constraint.AuthoredCoordinate {
	out := make([]constraint.AuthoredCoordinate, 0, len(a.keys))
	for _, key := range a.keys {
		out = append(out, constraint.AuthoredCoordinate{Value: key, SourceFile: a.file})
	}
	return out
}

var (
	_ authoredProvidesAxes = provAxes{}
	_ GraphCoordinates     = provAxes{}
)

// authoredProvidesFacts answers the one fact the rule reads and refuses every
// other, and counts the reads so a stopped walk is measured by the subjects it
// reached rather than by the error it returned.
//
// It embeds the presentation interface with no value behind it, so a rule reading
// a fact these proofs do not supply panics rather than answering a zero value.
type authoredProvidesFacts struct {
	constraint.DescribedView

	// text is what every subject's authored template answers, unless byKey names
	// that subject.
	text string

	// byKey answers per subject, keyed "node/key". nil means every subject answers
	// text.
	byKey map[string]string

	reads    *int
	withdraw func()
	// triggerAt is the 1-based read at which the caller withdraws, or 0 for a
	// caller that never does.
	triggerAt int
}

func (f authoredProvidesFacts) AuthoredNodeProvides(node string, key string) string {
	*f.reads++
	if f.triggerAt != 0 && *f.reads == f.triggerAt && f.withdraw != nil {
		f.withdraw()
	}
	if f.byKey != nil {
		return f.byKey[node+"/"+key]
	}
	return f.text
}

// providesSelfContainmentHalt asks the declared rule about one authored provides
// template, through the production invocation site rather than by calling the
// rule's predicate directly, and returns the halt it produced.
//
// It asserts the read happened. A helper that returned nil because the walk
// reached no subject would report exactly what an admitted template reports, so
// every verdict taken through here has the invocation measured behind it.
func providesSelfContainmentHalt(t *testing.T, templateText, recipeID string) error {
	t.Helper()
	reads := 0
	err := runAuthoredProvidesRule(
		context.Background(),
		constraint.DescribedView(authoredProvidesFacts{text: templateText, reads: &reads}),
		provAxes{nodes: []string{"api"}, keys: []string{"value"}, file: recipeID},
	)
	if reads != 1 {
		t.Fatalf("the rule read the authored text %d time(s), want exactly 1; a verdict from a walk that reached no subject certifies nothing", reads)
	}
	return err
}

// TestTheAuthoredProvidesRuleIsReachedFromTheRenderSite proves the invocation
// site reaches the rule at all, and that its finding carries the position an
// author navigates to.
func TestTheAuthoredProvidesRuleIsReachedFromTheRenderSite(t *testing.T) {
	t.Parallel()

	if err := providesSelfContainmentHalt(t, `{{ .context.instance_id }}-db`, "ok-recipe"); err != nil {
		t.Fatalf("the site refused a template reading only the self-contained data: %v", err)
	}

	err := providesSelfContainmentHalt(t, `{{ .context.inject.env.TARGET }}`, "bad-recipe")
	if err == nil {
		t.Fatal("the site admitted a template reaching outside the self-contained data")
	}
	if !errors.Is(err, ErrProvidesSelfContainment()) {
		t.Errorf("halt condition = %v, want the self-containment condition", err)
	}
	// The rendered diagnostic, position included: the Recipe the entry was written
	// in, the authored key inside it, and the expression to change.
	text := err.Error()
	for _, want := range []string{".context.inject.env.TARGET", "bad-recipe", ".spec.provides.value"} {
		if !strings.Contains(text, want) {
			t.Errorf("the halt omits %q, so an author cannot reach the entry to edit: %s", want, text)
		}
	}
}

// TestTheAuthoredProvidesRuleReportsEveryOffendingEntry is the diagnostic-count
// proof: an input carrying more than one offending entry produces one finding per
// entry, so an author corrects all of them from one build.
//
// The count is asserted against the number of offenders the fixture plants rather
// than against a literal, so a fixture that stopped planting several would fail
// here instead of quietly making the assertion trivial.
func TestTheAuthoredProvidesRuleReportsEveryOffendingEntry(t *testing.T) {
	t.Parallel()

	// Two nodes; the first offends in both its entries, the second in one and
	// admits the other. Three offenders, four subjects. Each offender is paired
	// with the expression its finding has to name, which is what the rule supplies
	// as its detail — the authored template is the input, the expression is the
	// part of it the author edits.
	offenders := map[string]string{
		"node-a/first":  ".context.inject.env.TARGET",
		"node-a/second": ".metadata.name",
		"node-b/first":  ".context.uses.db.provides.host",
	}
	byKey := map[string]string{}
	for k, expr := range offenders {
		byKey[k] = "{{ " + expr + " }}"
	}
	const admittedExpr = ".context.instance_id"
	byKey["node-b/second"] = "{{ " + admittedExpr + " }}"

	reads := 0
	err := runAuthoredProvidesRule(
		context.Background(),
		constraint.DescribedView(authoredProvidesFacts{byKey: byKey, reads: &reads}),
		provAxes{nodes: []string{"node-a", "node-b"}, keys: provKeyIDs(), file: "multi-recipe"},
	)
	if want := len(byKey); reads != want {
		t.Fatalf("the walk reached %d subject(s) of %d; the count below would not be over the whole fixture", reads, want)
	}
	if err == nil {
		t.Fatal("the site admitted an input carrying three offending entries")
	}

	// Each offender is its own diagnostic, joined under one envelope. Counting the
	// leaves is what distinguishes "reported every one" from "reported the first".
	leaves := haltLeafMessages(err)
	if len(leaves) != len(offenders) {
		t.Fatalf("the halt carries %d diagnostic(s) for %d offending entries: %v", len(leaves), len(offenders), leaves)
	}
	joined := strings.Join(leaves, "\n")
	for _, expr := range offenders {
		if !strings.Contains(joined, expr) {
			t.Errorf("no diagnostic names the offending expression %q: %s", expr, joined)
		}
	}
	if strings.Contains(joined, admittedExpr) {
		t.Errorf("a diagnostic names %q, which the rule admits: %s", admittedExpr, joined)
	}
	t.Logf("three offending entries across two nodes -> %d diagnostic(s)", len(leaves))
}

// TestTheAuthoredProvidesWalkStopsOnTheIterationAfterTheSignalTurns proves the
// invocation observes the caller's signal per iteration rather than reporting a
// withdrawal after a completed walk.
//
// # The trigger index is derived from THIS walk's order
//
// The walk is outermost by node and inner by provides key, and the signal is read
// once per node — as the first statement of the outer loop's body. So a withdrawal
// inside the FIRST node's first read is seen at the second node's signal read: the
// first node's remaining keys are read (they are inside the iteration already
// entered) and no later node is entered at all. The reached count is therefore
// exactly one node's worth of keys.
//
// # Why the count and not the error
//
// A walk that ran to the end and reported the withdrawal afterwards returns the
// same error as one that stopped partway. The exact equality below is what
// separates them, and the control beside it is what stops the pair passing on a
// walk that reaches nothing.
func TestTheAuthoredProvidesWalkStopsOnTheIterationAfterTheSignalTurns(t *testing.T) {
	t.Parallel()

	total := provNodeCount * provKeysPerNode
	if provNodeCount < 2 || provKeysPerNode < 1 {
		t.Fatalf("the fixture holds %d node(s) of %d key(s); one node's worth and the whole walk are the same number",
			provNodeCount, provKeysPerNode)
	}
	const offending = `{{ .context.inject.env.TARGET }}`
	axes := provAxes{nodes: provNodeIDs(), keys: provKeyIDs(), file: "cancel-recipe"}

	// CONTROL — an uncancellable caller reaches every key and the rule raises one
	// objection per key. Without this the assertions below would pass on a walk
	// that reached nothing.
	controlReads := 0
	controlErr := runAuthoredProvidesRule(
		context.Background(),
		constraint.DescribedView(authoredProvidesFacts{text: offending, reads: &controlReads}),
		axes,
	)
	if controlReads != total {
		t.Fatalf("the control walk reached %d key(s) of %d; the fixture does not present what these proofs assume",
			controlReads, total)
	}
	if controlErr == nil {
		t.Fatal("the control walk raised nothing over a fixture whose every entry is refused")
	}
	if leaves := haltLeafMessages(controlErr); len(leaves) != total {
		t.Fatalf("the control walk produced %d diagnostic(s) over %d refused entries", len(leaves), total)
	}

	// The withdrawal, taken on the first read.
	ctx, cancel := context.WithCancel(context.Background())
	reads := 0
	err := runAuthoredProvidesRule(
		ctx,
		constraint.DescribedView(authoredProvidesFacts{
			text: offending, reads: &reads, withdraw: cancel, triggerAt: 1,
		}),
		axes,
	)
	cancel()

	if reads != provKeysPerNode {
		t.Errorf("the stopped walk reached %d key(s), want exactly one node's worth (%d) out of %d; "+
			"reaching all %d would mean the signal is read after the walk rather than inside it",
			reads, provKeysPerNode, total, total)
	}
	if reads >= total {
		t.Errorf("the stopped walk reached %d key(s) of %d; it did not stop partway at all", reads, total)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("the stopped walk returned %v, want the caller's own withdrawal", err)
	}
	// A stopped walk hands back no findings: a partial set returned as though it
	// were complete would report a subset of the violations as all of them.
	if errors.Is(err, ErrProvidesSelfContainment()) {
		t.Errorf("the stopped walk returned a rule finding as well as the withdrawal: %v", err)
	}
}

// TestTheRenderEnumerationPresentsTheDeclaredProvidesKeys proves the enumeration
// the render supplies presents exactly the keys a bound Recipe declares, in the
// order that Recipe declares them.
//
// The rule is invoked once per key this axis yields, so an axis that yielded none
// would leave the rule invoked zero times — which reports exactly what a rule that
// ran and approved reports.
func TestTheRenderEnumerationPresentsTheDeclaredProvidesKeys(t *testing.T) {
	t.Parallel()

	provides := types.OrderedMap[string]{}
	provides.Set("zebra", `{{ .context.instance_id }}`)
	provides.Set("alpha", `{{ .values.port }}`)
	recipes := map[string]*types.Recipe{
		"db": {Metadata: types.RecipeMetadata{Name: "db-recipe"}, Spec: types.RecipeSpec{Provides: provides}},
	}
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("db", types.IRNode{})
	graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes}}

	coordinates := newDescribedStageCoordinates(graph, provAxes{nodes: []string{"db"}}, recipes, RecipeLookup{}, nil)
	if coordinates == nil {
		t.Fatal("the enumeration refused to build over a graph and a bound Recipe")
	}

	got := coordinates.ProvidesKeys("db")
	if len(got) != provides.Len() {
		t.Fatalf("the axis presents %d key(s) for a Recipe declaring %d", len(got), provides.Len())
	}
	// Declared order, not sorted order: the second key sorts first.
	for i, want := range provides.Keys() {
		if got[i].Value != want {
			t.Errorf("axis entry %d = %q, want %q — the axis is not in declared order", i, got[i].Value, want)
		}
		if got[i].SourceFile != "db-recipe" {
			t.Errorf("axis entry %d is positioned at %q, want the Recipe that declares it", i, got[i].SourceFile)
		}
	}
	if keys := coordinates.ProvidesKeys("absent"); keys != nil {
		t.Errorf("the axis presents %v for a node no Recipe was bound to", keys)
	}
}

// haltLeafMessages is the messages the leaf diagnostics of a halt carry: a joined
// set contributes its members, anything else contributes itself.
//
// The count is the thing under test in the proofs above, so it is derived from the
// error chain rather than from any statement about how many findings there should
// be.
func haltLeafMessages(err error) []string {
	leaves := haltLeaves(err)
	out := make([]string, 0, len(leaves))
	for _, leaf := range leaves {
		out = append(out, leaf.Error())
	}
	return out
}

func haltLeaves(err error) []error {
	type multi interface{ Unwrap() []error }
	probe := err
	for probe != nil {
		if m, ok := probe.(multi); ok {
			var out []error
			for _, member := range m.Unwrap() {
				out = append(out, haltLeaves(member)...)
			}
			return out
		}
		type single interface{ Unwrap() error }
		s, ok := probe.(single)
		if !ok {
			break
		}
		probe = s.Unwrap()
	}
	return []error{err}
}
