// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The proofs in this file cover the two rules declared over the text a Recipe
// wrote for each of its locals: that the production site reaches them, that they
// agree with the evaluation-time refusal they sit ahead of, that they report every
// offending entry where that refusal reports the first, that the walk observes the
// caller's cancellation signal per iteration, and that the presentation and the
// enumeration the render supplies answer the two facts they read.
//
// The agreement proof is the load-bearing one. The rules decide from declarations
// alone what the evaluation decides while resolving, so nothing but a measured
// comparison establishes that the two decide the same thing — and a rule that
// disagreed would either refuse a document the compiler admits today or admit one
// it refuses.

// localsRuleCase is one authored locals block plus what each of the two paths must
// do with it. want is the sentinel each path is expected to halt with, nil for a
// block both admit.
//
// Every row carries AT MOST ONE offending entry, and that is what makes the two
// readings comparable at all: the evaluation aborts its block at the first
// offender, while the declared rules are invoked once per entry and report every
// one — so a row with two offenders would compare a set against a singleton. The
// breadth difference is measured on its own, below.
type localsRuleCase struct {
	name   string
	locals []string
	want   error

	// evaluationHaltsOnAnotherCondition marks a row where the evaluation halts on
	// a condition that is NOT one of the two reference faults — a template that
	// does not parse — while the declared rules correctly decline it. Without the
	// flag such a row would read as two paths agreeing on silence, when one of
	// them halted.
	evaluationHaltsOnAnotherCondition bool
}

func localsRuleCases() []localsRuleCase {
	return []localsRuleCase{
		{
			name:   "an entry reading itself is the ordering class",
			locals: []string{"loop", "{{ .locals.loop }}-x"},
			want:   ErrLocalsForwardReference(),
		},
		{
			name: "a chain of three in author order resolves",
			locals: []string{
				"stack", "edge",
				"svc", "{{ .locals.stack }}-gateway",
				"fqdn", "{{ .locals.svc }}.{{ .locals.stack }}.internal",
			},
		},
		{
			// The same three entries with the chain reversed. A form that decided
			// the question by collapsing the order would admit this and would
			// therefore agree on the row above while disagreeing here.
			name: "a chain of three reversed halts on the ordering class",
			locals: []string{
				"fqdn", "{{ .locals.svc }}.{{ .locals.stack }}.internal",
				"svc", "{{ .locals.stack }}-gateway",
				"stack", "edge",
			},
			want: ErrLocalsForwardReference(),
		},
		{
			name:   "a forward reference by one entry",
			locals: []string{"derived", "{{ .locals.base }}.internal", "base", "svc"},
			want:   ErrLocalsForwardReference(),
		},
		{
			name:   "a key the block never declares is the naming class",
			locals: []string{"base", "svc", "derived", "{{ .locals.bass }}.internal"},
			want:   ErrLocalsUnknownReference(),
		},
		{
			name:   "a backward reference is legal",
			locals: []string{"base", "svc", "derived", "{{ .locals.base }}.internal"},
		},
		{
			name:   "a bare pillar reference is admitted",
			locals: []string{"guard", "{{ if .locals }}yes{{ end }}"},
		},
		{
			name:   "a block reading no local at all",
			locals: []string{"plain", "static-value"},
		},
		{
			// The polarity trap, stated as a row rather than as a comment. The
			// element of a range body is not the locals pillar, so this reads a
			// field of the element and the compiler admits it. A form sourced from
			// the over-collecting walk attributes ".locals.later" here and refuses
			// a template the template language permits.
			name:   "a read inside a rebound range body is not a locals reference",
			locals: []string{"k", "{{ range .values.list }}{{ .locals.later }}{{ end }}"},
		},
		{
			name:   "the same, naming a key the block does not declare either",
			locals: []string{"k", "{{ range .values.list }}{{ .locals.nowhere }}{{ end }}"},
		},
		{
			name:   "the pillar's name inside a string literal is not a reference",
			locals: []string{"k", `{{ printf "%s" ".locals.nope" }}`},
		},
		{
			// The one row where the two paths legitimately differ, and it is not a
			// disagreement about the rule: the evaluation halts on the template's own
			// parse failure, while the declared rules decline it because evaluation
			// already reports it against the same entry and refusing it here as well
			// would report one authoring fault twice.
			name:                              "a malformed template is the evaluator's diagnostic, not a reference fault",
			locals:                            []string{"k", "{{ .locals.base "},
			evaluationHaltsOnAnotherCondition: true,
		},
	}
}

// localsRuleRecipe is the authored document both paths are driven with, so the
// comparison is over one Recipe rather than over two fixtures that agree today.
func localsRuleRecipe(locals ...string) *types.Recipe {
	r := &types.Recipe{}
	r.Metadata.Name = "locals-rule-recipe"
	r.Spec.Locals = omLocals(locals...)
	return r
}

// localsRuleValues is the pillar the range rows iterate. It carries an element
// whose own field is spelled like the locals pillar, so a form that attributed the
// read would have something to attribute it to rather than failing to resolve.
func localsRuleValues() types.OrderedMap[any] {
	values := types.OrderedMap[any]{}
	values.Set("list", []any{map[string]any{"locals": map[string]any{
		"later": "ELEMENT-FIELD", "nowhere": "ELEMENT-FIELD",
	}}})
	return values
}

// declaredLocalsHalt drives the two declared rules through the PRODUCTION
// invocation site over one authored Recipe, with the real presentation and the
// real enumeration, and returns the halt.
//
// Both sides of the comparison are production code: the presentation is the one
// the render builds, and the axis is the one the render derives. A fixture
// answering the two facts itself would compare the evaluation against a second
// reading of the Recipe rather than against the declared route.
func declaredLocalsHalt(t *testing.T, recipe *types.Recipe) error {
	t.Helper()
	const node = "api"
	recipes := map[string]*types.Recipe{node: recipe}

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(node, types.IRNode{})
	graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes}}

	view := newDescribedView(
		context.Background(), graph, stubGraphFacts{},
		recipes, RecipeLookup{}, nil, newStepDescriptorMemo(), nil, nil,
		nil, nil, nil,
	)
	if view == nil {
		t.Fatal("the presentation was not built; there is nothing for the rules to read")
	}
	axes := newDescribedStageCoordinates(graph, localsAxis{nodes: []string{node}}, recipes, RecipeLookup{}, nil)
	if axes == nil {
		t.Fatal("the enumeration refused to build over a graph and a bound Recipe")
	}
	// A verdict from a walk that reached no subject reports exactly what an
	// admitted block reports, so every comparison taken through here has the
	// subject count measured behind it.
	if got, want := len(axes.LocalsKeys(node)), recipe.Spec.Locals.Len(); got != want {
		t.Fatalf("the axis presents %d local(s) for a Recipe declaring %d; the verdict below would certify nothing", got, want)
	}
	return runAuthoredLocalsRule(context.Background(), view, axes)
}

// localsFaultClass names which of the two locals conditions a path reported, so a
// compared cell names the class rather than a whole wrapped chain.
//
// A halt carrying NEITHER condition reads as "none", and that is the criterion
// rather than a leniency in it: the comparison is about which of the two reference
// faults each path reports, and a template that does not parse is a third thing
// entirely — the evaluator's own diagnostic, which the declared rules decline by
// design because refusing it as well would report one authoring fault twice. The
// row exercising that case asserts the evaluation halts on its own condition
// separately, so "none" on both sides there is not two silences agreeing.
func localsFaultClass(err error) string {
	switch {
	case errors.Is(err, ErrLocalsForwardReference()) && errors.Is(err, ErrLocalsUnknownReference()):
		return "both"
	case errors.Is(err, ErrLocalsForwardReference()):
		return "ordering"
	case errors.Is(err, ErrLocalsUnknownReference()):
		return "naming"
	default:
		return "none"
	}
}

// TestTheDeclaredLocalsRulesDecideWhatTheEvaluationDecides is the agreement
// measurement, and it is what licenses declaring the rule at all.
//
// # Criterion
//
// For each authored block: whether the path halts, and which of the two condition
// classes the halt carries. The evaluation side is [EvaluateRecipeLocals], the
// declared side is the production invocation site over the real presentation and
// the real axis. Every row carries at most one offending entry, so the
// evaluation's first-offender reading and the declared rules' whole-set reading
// name the same class.
//
// # Calibration
//
// The comparison is calibrated in the test below, which reports a difference for a
// deliberately wrong declared verdict — so an agreement here is a claim the
// instrument is capable of contradicting.
func TestTheDeclaredLocalsRulesDecideWhatTheEvaluationDecides(t *testing.T) {
	t.Parallel()
	values := localsRuleValues()
	for _, tc := range localsRuleCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, evaluated := EvaluateRecipeLocals(
				localsRuleRecipe(tc.locals...), "api", values, types.OrderedMap[any]{},
			)
			declared := declaredLocalsHalt(t, localsRuleRecipe(tc.locals...))

			evaluatedClass := localsFaultClass(evaluated)
			declaredClass := localsFaultClass(declared)
			if evaluatedClass != declaredClass {
				t.Errorf("the two paths disagree: evaluation=%s declared=%s\n  evaluation: %v\n  declared:   %v",
					evaluatedClass, declaredClass, evaluated, declared)
			}
			// The row's own expectation, so a row that stopped exercising what it
			// names fails here rather than making the agreement trivial.
			if want := localsFaultClass(tc.want); evaluatedClass != want {
				t.Errorf("the evaluation reported %s, the row expects %s: %v", evaluatedClass, want, evaluated)
			}
			// A row expecting NO locals fault must still say whether the evaluation
			// halted at all, or "none on both sides" cannot be told from two paths
			// that were never asked.
			if tc.want == nil {
				if tc.evaluationHaltsOnAnotherCondition && evaluated == nil {
					t.Errorf("the row claims the evaluation halts on its own condition, and it admitted the block")
				}
				if !tc.evaluationHaltsOnAnotherCondition && evaluated != nil {
					t.Errorf("the evaluation halted on a condition the row does not name: %v", evaluated)
				}
				if declared != nil {
					t.Errorf("the declared rules refused a block the evaluation admits: %v", declared)
				}
			}
			t.Logf("CELL %-30s | evaluation=%-9s declared=%-9s", tc.name, evaluatedClass, declaredClass)
		})
	}
}

// TestTheLocalsAgreementComparisonCanReportADifference is the needle under the
// proof above: a declared verdict deliberately taken from the WRONG entry's
// position in the block must compare as a difference.
//
// Without it, an agreement over rows the declared side happened never to be asked
// about would read exactly like an agreement over rows it decided correctly.
func TestTheLocalsAgreementComparisonCanReportADifference(t *testing.T) {
	t.Parallel()
	// A forward reference by one entry: the evaluation halts on the ordering class.
	locals := []string{"derived", "{{ .locals.base }}.internal", "base", "svc"}
	_, evaluated := EvaluateRecipeLocals(
		localsRuleRecipe(locals...), "api", localsRuleValues(), types.OrderedMap[any]{},
	)
	if localsFaultClass(evaluated) != "ordering" {
		t.Fatalf("the needle's input no longer halts on the ordering class: %v", evaluated)
	}

	// The real declared verdict for the offending entry, and the neutered one: the
	// same classification asked as though the entry sat LAST in the block, which is
	// what a form that collapsed author order would decide. The neutered form has to
	// answer differently, or the comparison above could not report a difference at
	// all.
	const text = "{{ .locals.base }}.internal"
	order := []string{"derived", "base"}
	actual := localsReferenceFault(order, "derived", text)
	// The neutered form: the WHOLE declared set treated as readable, which is what a
	// rule that ignored author order would decide. It admits what the real form
	// refuses, so the comparison is capable of reporting a difference.
	neutered := gotmpl.ClassifyRootKeyReference(text, "{{", "}}", localsContextRoot, order, order)
	if actual.Fault == neutered.Fault {
		t.Fatalf("the neutered form agreed with the real one (%v); the comparison cannot report a difference",
			actual.Fault)
	}
	if actual.Fault != gotmpl.RootKeyUnresolved || neutered.Fault != gotmpl.RootKeyAdmitted {
		t.Fatalf("the calibration pair is not the one it claims: real=%v neutered=%v", actual.Fault, neutered.Fault)
	}
	t.Logf("calibration: real=%v neutered=%v -> DIFFER as expected", actual.Fault, neutered.Fault)
}

// TestTheDeclaredLocalsRulesReportEveryOffendingEntry is the breadth difference,
// measured rather than claimed: the evaluation aborts its block at the first
// offender and the declared rules report every one, so an author corrects all of
// them from one build.
//
// This is the one property the migration ADDS, and it is why the two readings are
// compared over single-offender rows above: over this input they legitimately
// differ in count while agreeing on what they refuse.
func TestTheDeclaredLocalsRulesReportEveryOffendingEntry(t *testing.T) {
	t.Parallel()
	// Three entries, two of them offending in different classes, so the count and
	// the classes are both observable.
	locals := []string{
		"first", "{{ .locals.third }}", // ordering: third is declared below
		"second", "{{ .locals.absent }}", // naming: never declared
		"third", "ok",
	}

	_, evaluated := EvaluateRecipeLocals(
		localsRuleRecipe(locals...), "api", localsRuleValues(), types.OrderedMap[any]{},
	)
	if got := localsFaultClass(evaluated); got != "ordering" {
		t.Fatalf("the evaluation reported %s; this input's first offender is the ordering class: %v", got, evaluated)
	}
	if errors.Is(evaluated, ErrLocalsUnknownReference()) {
		t.Error("the evaluation reported the second offender as well; the breadth difference below measures nothing")
	}

	declared := declaredLocalsHalt(t, localsRuleRecipe(locals...))
	if got := localsFaultClass(declared); got != "both" {
		t.Fatalf("the declared rules reported %s; both offenders must be reported: %v", got, declared)
	}
	leaves := haltLeafMessages(declared)
	if len(leaves) != 2 {
		t.Fatalf("the declared halt carries %d diagnostic(s) for 2 offending entries: %v", len(leaves), leaves)
	}
	joined := strings.Join(leaves, "\n")
	for _, want := range []string{".locals.third", ".locals.absent"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no diagnostic names the offending expression %q: %s", want, joined)
		}
	}
	t.Logf("evaluation reported 1 offender, the declared rules reported %d", len(leaves))
}

// TestTheAuthoredLocalsHaltCarriesItsPositionAndItsRemedy is the diagnostic proof.
//
// The remedy is the reason the framework's raise carries an advice channel at all:
// a self-reference is the SAME condition as any other ordering fault, and no
// reordering fixes it — so the registered sentence cannot state what to do and the
// occurrence has to.
func TestTheAuthoredLocalsHaltCarriesItsPositionAndItsRemedy(t *testing.T) {
	t.Parallel()

	forward := declaredLocalsHalt(t, localsRuleRecipe("derived", "{{ .locals.base }}.internal", "base", "svc"))
	if forward == nil {
		t.Fatal("the site admitted a forward reference")
	}
	forwardText := forward.Error()
	for _, want := range []string{
		".locals.base",                  // the offending expression
		".spec.locals.derived",          // the position an author navigates to
		"locals-rule-recipe",            // the document it is in
		`define "base" above "derived"`, // the remedy for THIS occurrence
	} {
		if !strings.Contains(forwardText, want) {
			t.Errorf("the ordering halt omits %q: %s", want, forwardText)
		}
	}
	t.Logf("forward: %s", forwardText)

	self := declaredLocalsHalt(t, localsRuleRecipe("loop", "{{ .locals.loop }}-x"))
	if self == nil {
		t.Fatal("the site admitted a self-reference")
	}
	selfText := self.Error()
	if !strings.Contains(selfText, "cannot read itself") {
		t.Errorf("the self-reference halt does not carry the remedy that applies to it: %s", selfText)
	}
	// And it must NOT carry the reordering remedy, which is the whole reason the
	// remedy travels with the occurrence rather than with the condition.
	if strings.Contains(selfText, "above") {
		t.Errorf("the self-reference halt offers the reordering remedy, which cannot fix it: %s", selfText)
	}
	t.Logf("self:    %s", selfText)

	unknown := declaredLocalsHalt(t, localsRuleRecipe("base", "svc", "derived", "{{ .locals.bass }}.internal"))
	if unknown == nil {
		t.Fatal("the site admitted a reference to an undeclared key")
	}
	unknownText := unknown.Error()
	for _, want := range []string{".locals.bass", ".spec.locals.derived", "correct the key name", ".locals.base"} {
		if !strings.Contains(unknownText, want) {
			t.Errorf("the naming halt omits %q: %s", want, unknownText)
		}
	}
	if strings.Contains(unknownText, "author order") {
		t.Errorf("the naming halt offers the ordering remedy, which does not apply: %s", unknownText)
	}
	t.Logf("unknown: %s", unknownText)
}

// localsAxis presents a node set and the locals each node's Recipe declares, for
// the proofs that need to control the axis rather than derive it.
type localsAxis struct {
	nodes []string
	keys  []string
	file  string

	// attachments is the per-node attachment set, empty for the proofs that
	// present none. It is supplied rather than derived because the attachment
	// axis is what the node-carried attachment rules are invoked over: a fixture
	// answering nil here yields no subject, and a rule invoked zero times reports
	// exactly what a rule that ran and approved reports.
	attachments []string
}

func (a localsAxis) Nodes() []constraint.NodeInstance {
	out := make([]constraint.NodeInstance, 0, len(a.nodes))
	for _, id := range a.nodes {
		out = append(out, constraint.NodeInstance{ID: id, SourceFile: "aggregate.yaml", AuthoredName: id})
	}
	return out
}

// Attachments answers whatever the fixture was given, and the boundary, locals
// and connection axes beneath it answer empty. The methods are present because the
// coordinate set names every axis the stage's rules are keyed by, which is what
// stops a fixture silently omitting one.
//
// The distinction matters to the read counts the proofs below take. A fixture
// given no attachment yields no attachment subject, so the two attachment-borne
// locals rules are invoked zero times — which is why the fact presentation beside
// this one, whose embedded view has no value behind it, does not panic on a fact it
// does not answer. A fixture that DOES present an attachment routes the locals
// question on through the enumeration built over the render's own products, which
// is where the attachment-borne proofs take it.
func (a localsAxis) Attachments(_ string) []string { return a.attachments }

func (a localsAxis) Edges() []constraint.EdgeInstance { return nil }

func (a localsAxis) Boundaries() []constraint.AuthoredCoordinate { return nil }

func (a localsAxis) BoundaryAttachments(_ string) []string { return nil }

func (a localsAxis) AttachmentLocalsKeys(_, _ string) []constraint.AuthoredCoordinate { return nil }

func (a localsAxis) BoundaryAttachmentLocalsKeys(_, _ string) []constraint.AuthoredCoordinate {
	return nil
}

func (a localsAxis) LocalsKeys(_ string) []constraint.AuthoredCoordinate {
	out := make([]constraint.AuthoredCoordinate, 0, len(a.keys))
	for _, key := range a.keys {
		out = append(out, constraint.AuthoredCoordinate{Value: key, SourceFile: a.file})
	}
	return out
}

var (
	_ authoredLocalsAxes = localsAxis{}
	_ GraphCoordinates   = localsAxis{}
)

// authoredLocalsFacts answers the two facts the rules read and refuses every
// other, and counts the reads so a stopped walk is measured by the subjects it
// reached rather than by the error it returned.
//
// It embeds the presentation interface with no value behind it, so a rule reading a
// fact these proofs do not supply panics rather than answering a zero value.
type authoredLocalsFacts struct {
	constraint.DescribedView

	declared []string
	byKey    map[string]string

	reads    *int
	withdraw func()
	// triggerAt is the 1-based read at which the caller withdraws, or 0 for a
	// caller that never does.
	triggerAt int
}

func (f authoredLocalsFacts) DeclaredLocalsKeys(_ string) []string { return f.declared }

func (f authoredLocalsFacts) AuthoredNodeLocals(node string, key string) string {
	*f.reads++
	if f.triggerAt != 0 && *f.reads == f.triggerAt && f.withdraw != nil {
		f.withdraw()
	}
	return f.byKey[node+"/"+key]
}

// TestTheAuthoredLocalsWalkStopsOnTheIterationAfterTheSignalTurns proves the
// invocation observes the caller's signal per iteration rather than reporting a
// withdrawal after a completed walk.
//
// # The trigger index is derived from THIS walk's order
//
// The walk is outermost by node and inner by locals key, and the signal is read
// once per node — as the first statement of the outer loop's body. So a withdrawal
// inside the FIRST node's first read is seen at the second node's signal read: the
// first node's remaining keys are read and no later node is entered. The reached
// count is therefore exactly one node's worth of keys.
//
// # Why the count and not the error
//
// A walk that ran to the end and reported the withdrawal afterwards returns the
// same error as one that stopped partway. The exact equality below separates them,
// and the control beside it stops the pair passing on a walk that reaches nothing.
func TestTheAuthoredLocalsWalkStopsOnTheIterationAfterTheSignalTurns(t *testing.T) {
	t.Parallel()

	nodes := []string{"node-a", "node-b", "node-c"}
	keys := []string{"first", "second"}
	perNode := len(keys)
	total := len(nodes) * perNode
	if len(nodes) < 2 || perNode < 1 {
		t.Fatalf("the fixture holds %d node(s) of %d key(s); one node's worth and the whole walk are the same number",
			len(nodes), perNode)
	}
	axes := localsAxis{nodes: nodes, keys: keys, file: "cancel-recipe"}
	// Every entry names a key the block declares BELOW it, so every subject is
	// refused and a walk that reached fewer subjects raised fewer objections.
	byKey := map[string]string{}
	for _, node := range nodes {
		byKey[node+"/first"] = "{{ .locals.second }}"
		byKey[node+"/second"] = "{{ .locals.first }}"
	}
	// "first" reads "second", which is declared below it — the ordering fault.
	// "second" reads "first", which is above it — legal. So one offender per node.
	declared := keys

	// CONTROL — an uncancellable caller reaches every key. Without this the
	// assertions below would pass on a walk that reached nothing.
	controlReads := 0
	controlErr := runAuthoredLocalsRule(
		context.Background(),
		constraint.DescribedView(authoredLocalsFacts{declared: declared, byKey: byKey, reads: &controlReads}),
		axes,
	)
	// Each of the two rules walks the axis once, so a complete pair of walks reads
	// every subject twice. Stating it as a multiple of the axis size rather than as
	// a literal is what keeps the number derived from the fixture.
	if want := total * 2; controlReads != want {
		t.Fatalf("the control walk reached %d key-read(s) of %d; the fixture does not present what these proofs assume",
			controlReads, want)
	}
	if controlErr == nil {
		t.Fatal("the control walk raised nothing over a fixture whose every node carries a refused entry")
	}
	if leaves := haltLeafMessages(controlErr); len(leaves) != len(nodes) {
		t.Fatalf("the control walk produced %d diagnostic(s) for %d refused entries", len(leaves), len(nodes))
	}

	// The withdrawal, taken on the first read.
	ctx, cancel := context.WithCancel(context.Background())
	reads := 0
	err := runAuthoredLocalsRule(
		ctx,
		constraint.DescribedView(authoredLocalsFacts{
			declared: declared, byKey: byKey, reads: &reads, withdraw: cancel, triggerAt: 1,
		}),
		axes,
	)
	cancel()

	if reads != perNode {
		t.Errorf("the stopped walk reached %d key-read(s), want exactly one node's worth (%d); "+
			"reaching all %d would mean the signal is read after the walk rather than inside it",
			reads, perNode, total*2)
	}
	if reads >= total {
		t.Errorf("the stopped walk reached %d key-read(s) of %d; it did not stop partway at all", reads, total*2)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("the stopped walk returned %v, want the caller's own withdrawal", err)
	}
	// A stopped walk hands back no findings: a partial set returned as though it
	// were complete would report a subset of the violations as all of them.
	if errors.Is(err, ErrLocalsForwardReference()) {
		t.Errorf("the stopped walk returned a rule finding as well as the withdrawal: %v", err)
	}
}

// TestTheRenderEnumerationPresentsTheDeclaredLocals proves the enumeration the
// render supplies presents exactly the locals a bound Recipe declares, in the
// order that Recipe declares them — which is the order the rules' whole question
// is decided in.
func TestTheRenderEnumerationPresentsTheDeclaredLocals(t *testing.T) {
	t.Parallel()

	locals := omLocals("zebra", "z", "alpha", "{{ .locals.zebra }}")
	recipes := map[string]*types.Recipe{
		"db": {Metadata: types.RecipeMetadata{Name: "db-recipe"}, Spec: types.RecipeSpec{Locals: locals}},
	}
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("db", types.IRNode{})
	graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes}}

	coordinates := newDescribedStageCoordinates(graph, localsAxis{nodes: []string{"db"}}, recipes, RecipeLookup{}, nil)
	if coordinates == nil {
		t.Fatal("the enumeration refused to build over a graph and a bound Recipe")
	}

	got := coordinates.LocalsKeys("db")
	if len(got) != locals.Len() {
		t.Fatalf("the axis presents %d local(s) for a Recipe declaring %d", len(got), locals.Len())
	}
	// Declared order, not sorted order: the second key sorts first, and sorting
	// would make the backward reference above read as a forward one.
	for i, want := range locals.Keys() {
		if got[i].Value != want {
			t.Errorf("axis entry %d = %q, want %q — the axis is not in declared order", i, got[i].Value, want)
		}
		if got[i].SourceFile != "db-recipe" {
			t.Errorf("axis entry %d is positioned at %q, want the Recipe that declares it", i, got[i].SourceFile)
		}
	}
	if keys := coordinates.LocalsKeys("absent"); keys != nil {
		t.Errorf("the axis presents %v for a node no Recipe was bound to", keys)
	}
}

// TestTheDescribedPresentationAnswersTheTwoAuthoredLocalsFacts proves the
// presentation answers both facts from the Recipe the render bound, and answers a
// node it holds no Recipe for as one that declared nothing.
//
// The two facts are asserted together because they are answered from one ordered
// mapping: a key the order names is a key the text can be read for, and a
// presentation answering them from two sources would be the second producer the
// design forbids.
func TestTheDescribedPresentationAnswersTheTwoAuthoredLocalsFacts(t *testing.T) {
	t.Parallel()

	locals := omLocals("host", "db.internal", "port", "5432")
	recipes := map[string]*types.Recipe{
		"db": {Metadata: types.RecipeMetadata{Name: "db-recipe"}, Spec: types.RecipeSpec{Locals: locals}},
	}
	view := newDescribedView(
		context.Background(), &types.IRGraph{}, stubGraphFacts{},
		recipes, RecipeLookup{}, nil, newStepDescriptorMemo(), nil, nil,
		nil, nil, nil,
	)
	if view == nil {
		t.Fatal("the presentation was not built")
	}

	if got, want := view.DeclaredLocalsKeys("db"), locals.Keys(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("the key order fact answers %v, want the Recipe's own author order %v", got, want)
	}
	for _, key := range locals.Keys() {
		want, _ := locals.Get(key)
		if got := view.AuthoredNodeLocals("db", key); got != want {
			t.Errorf("the text fact answers %q for %q, want %q", got, key, want)
		}
	}
	if got := view.DeclaredLocalsKeys("absent"); got != nil {
		t.Errorf("a node with no bound Recipe answers %v, want no keys", got)
	}
	if got := view.AuthoredNodeLocals("absent", "host"); got != "" {
		t.Errorf("a node with no bound Recipe answers %q, want empty", got)
	}
	if got := view.AuthoredNodeLocals("db", "no-such-key"); got != "" {
		t.Errorf("a key the Recipe does not declare answers %q, want empty", got)
	}
}

// TestOneSharedBlocksFaultNamesEveryComponentItWasReachedThrough is the proof for
// the caption that tells repeated findings apart.
//
// # What it measures
//
// One authored locals block, bound by several components, carrying one offending
// entry. Every component that binds it yields a finding, so the criterion is that
// the findings are as many as the components, that each names a different one, and
// that between them they name every component — a set difference rather than a
// count, since two sides short by one still agree on a count.
//
// # Why the messages are compared with the component removed
//
// The load-bearing property is not that the component appears somewhere: it is
// that the component is the ONLY thing separating these findings. They are about
// one authored entry in one document, so they share a position and every other
// value. Cutting each message at the caption and requiring the remainders to be
// identical states exactly that, and it is a claim the comparison can contradict —
// a rule that varied anything else would leave the remainders differing.
func TestOneSharedBlocksFaultNamesEveryComponentItWasReachedThrough(t *testing.T) {
	t.Parallel()

	nodes := []string{"alpha", "bravo", "charlie"}
	const offending, legal = "first", "second"
	// "first" reads "second", which the block declares BELOW it — one offender per
	// component, and the same authored text for every one of them, which is what
	// makes this the shared-document case rather than several documents.
	byKey := map[string]string{}
	for _, node := range nodes {
		byKey[node+"/"+offending] = "{{ .locals." + legal + " }}"
		byKey[node+"/"+legal] = "static"
	}
	axes := localsAxis{nodes: nodes, keys: []string{offending, legal}, file: "shared-recipe"}

	reads := 0
	err := runAuthoredLocalsRule(
		context.Background(),
		constraint.DescribedView(authoredLocalsFacts{
			declared: []string{offending, legal}, byKey: byKey, reads: &reads,
		}),
		axes,
	)
	if err == nil {
		t.Fatal("the walk admitted a block whose every component carries a refused entry")
	}
	leaves := haltLeafMessages(err)
	if len(leaves) != len(nodes) {
		t.Fatalf("the walk produced %d finding(s) for %d component(s) binding one faulty block; "+
			"a count that is not one per component means they were folded or the walk reached fewer",
			len(leaves), len(nodes))
	}

	// Which component each finding names, and the remainder once that is cut away.
	const caption = `reached through the component "`
	named := map[string]bool{}
	remainders := map[string][]string{}
	for _, msg := range leaves {
		at := strings.Index(msg, caption)
		if at < 0 {
			t.Fatalf("a finding does not name the component it was reached through: %s", msg)
		}
		rest := msg[at+len(caption):]
		end := strings.Index(rest, `"`)
		if end < 0 {
			t.Fatalf("the component caption is not closed: %s", msg)
		}
		component := rest[:end]
		if named[component] {
			t.Errorf("two findings name the same component %q; they cannot be told apart", component)
		}
		named[component] = true
		stripped := msg[:at] + msg[at+len(caption)+end+1:]
		remainders[stripped] = append(remainders[stripped], component)
	}
	for _, node := range nodes {
		if !named[node] {
			t.Errorf("no finding names the component %q, so its author is not told it is affected", node)
		}
	}
	// The component is the only difference: one remainder, shared by every finding.
	if len(remainders) != 1 {
		t.Errorf("the findings differ in %d way(s) besides the component; the caption is supposed to be "+
			"the whole of what separates them: %v", len(remainders), remainders)
	}
	for _, msg := range leaves {
		t.Logf("FINDING %s", msg)
	}
}

// TestBothNodeLocalsRulesWordTheComponentCaptionIdentically holds the two
// conditions to one wording, which is the half of single-sourcing a test can
// observe.
//
// The two rules report different faults and compose their own details, so a copy
// of the caption in either would be invisible from any message assertion that only
// looked for the component. Comparing the wording the two produce fails the moment
// one is reworded and the other is not.
//
// # What this deliberately does NOT establish
//
// Agreement is what a copy also produces on the day it is written, so this cannot
// show the caption has one home. That is established off the code instead: the
// wording occurs once outside the tests, and every rule rendering it resolves to
// the one function composing it.
//
// The two rules keyed at a boundary are absent here, and their absence is enforced
// by the compiler rather than by this test: such a block is written once against a
// boundary and reaches every component the boundary holds, so it has no repeated
// finding to separate and no single component it could name.
func TestBothNodeLocalsRulesWordTheComponentCaptionIdentically(t *testing.T) {
	t.Parallel()

	const node = "alpha"
	byKey := map[string]string{
		node + "/forward": "{{ .locals.later }}",
		node + "/later":   "static",
		node + "/unknown": "{{ .locals.absent }}",
	}
	// captionOf returns the words the rule put in front of the quoted component,
	// taken from the finding rather than typed here — a hand-typed expectation
	// would be the very copy the shared composition exists to prevent.
	captionOf := func(t *testing.T, keys []string) string {
		t.Helper()
		reads := 0
		err := runAuthoredLocalsRule(
			context.Background(),
			constraint.DescribedView(authoredLocalsFacts{
				declared: []string{"forward", "later", "unknown"}, byKey: byKey, reads: &reads,
			}),
			localsAxis{nodes: []string{node}, keys: keys, file: "shared-recipe"},
		)
		if err == nil {
			t.Fatal("the rule admitted the offending entry, so nothing here is measured")
		}
		leaves := haltLeafMessages(err)
		if len(leaves) != 1 {
			t.Fatalf("the fixture produced %d finding(s); want exactly one", len(leaves))
		}
		quoted := `"` + node + `"`
		at := strings.LastIndex(leaves[0], quoted)
		if at < 0 {
			t.Fatalf("the finding does not name the component at all: %s", leaves[0])
		}
		// Back up to the separator the composition puts between detail values, so
		// what is compared is this value's own caption and not the whole message.
		head := leaves[0][:at]
		if sep := strings.LastIndex(head, ", "); sep >= 0 {
			head = head[sep+2:]
		}
		return strings.TrimSpace(head)
	}

	ordering := captionOf(t, []string{"forward", "later"})
	naming := captionOf(t, []string{"unknown"})
	if ordering == "" {
		t.Fatal("the component was raised with no caption at all; a bare identifier in a captioned list reads as noise")
	}
	if ordering != naming {
		t.Errorf("the two rules word the component caption differently, so one of them carries its own copy:\n"+
			"  ordering rule: %q\n  naming rule:   %q", ordering, naming)
	}
	t.Logf("both rules caption the component as %q", ordering)
}
