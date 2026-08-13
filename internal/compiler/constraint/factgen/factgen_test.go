// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// inRepoTargets maps each target whose committed copy lives inside this module to
// the directory holding it, relative to this test package.
var inRepoTargets = map[string][]string{
	"views_gen.go":        {".."},
	"subjectkinds_gen.go": {".."},
}

// externalTargets names each target whose committed copy lives outside this
// module, so it is unreadable in a standalone checkout of this repository and
// cannot be drift-gated from here. Naming them is what lets the gate below
// account for every registered target: a new target that is neither read here nor
// named here fails the coverage check rather than being silently skipped.
var externalTargets = map[string]bool{
	"constraint-placement-table.md": true,
}

// TestRegenClean is the regeneration-drift gate for every target committed inside
// this module: re-rendering from the registry must byte-match the committed file.
// A failure means the registry (or the generator) changed without regenerating.
//
// This is what makes a placement change reviewable. Moving a fact to another
// stage is one line in the registry, and its consequences — a method moving
// between view interfaces, a row moving in the placement table, a subject kind
// appearing or disappearing — arrive as a diff rather than as a judgement a
// reader would have to reconstruct.
//
// A target committed outside this module is deliberately not read here, so this
// suite passes in a standalone checkout of this repository, where that file is
// absent. Such a target is drift-gated by the workspace build, which has every
// tree present and regenerates the target into a throwaway path to diff it.
func TestRegenClean(t *testing.T) {
	t.Parallel()
	for base, dir := range inRepoTargets {
		t.Run(base, func(t *testing.T) {
			t.Parallel()
			got, err := generateFor(base)
			if err != nil {
				t.Fatalf("generate %s: %v", base, err)
			}
			path := filepath.Join(append(append([]string{}, dir...), base)...)
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read committed %s: %v", path, err)
			}
			if string(got) != string(want) {
				t.Errorf("%s is stale: generator output differs from the committed file.\n"+
					"Run `go generate ./internal/compiler/constraint/factgen` and commit the result.", base)
			}
		})
	}
}

// TestRegenGateCoversEveryTarget closes the hole narrowing the gate above opened:
// once the gate walks a hand-listed subset rather than the target registry, a
// target added to the registry and forgotten here would be drift-gated by nothing
// and no test would say so. Every registered target must be accounted for as
// either read in-repo or gated by the workspace build, and the two sets must not
// overlap or claim a target that is not registered.
func TestRegenGateCoversEveryTarget(t *testing.T) {
	t.Parallel()
	for base := range targets {
		_, inRepo := inRepoTargets[base]
		if !inRepo && !externalTargets[base] {
			t.Errorf("target %q is registered but neither drift-gated in-repo nor named as "+
				"gated by the workspace build, so nothing checks it for staleness", base)
		}
		if inRepo && externalTargets[base] {
			t.Errorf("target %q is claimed both in-repo and external", base)
		}
	}
	for base := range inRepoTargets {
		if _, ok := targets[base]; !ok {
			t.Errorf("%q has a committed in-repo path but is not a registered target", base)
		}
	}
	for base := range externalTargets {
		if _, ok := targets[base]; !ok {
			t.Errorf("%q is named as externally gated but is not a registered target", base)
		}
	}
}

// TestChainDerivesLinearly pins the two properties the emitters rely on and the
// generator refuses to proceed without: the declared stage relation is a single
// chain, and every stage on it is occupied.
//
// The refusal itself is the enforcement — generation halts, so no chain with an
// unordered pair in it can be emitted or committed. This test states the
// satisfied case, so a registry edit that breaks it fails here as well as at
// generation.
func TestChainDerivesLinearly(t *testing.T) {
	t.Parallel()
	c, err := deriveChain()
	if err != nil {
		t.Fatalf("the stage chain does not derive: %v", err)
	}
	if len(c.order) != len(stages) {
		t.Fatalf("the chain holds %d stages, %d are declared", len(c.order), len(stages))
	}
	for i, s := range c.order {
		if i == 0 {
			if s.Extends != "" {
				t.Errorf("the chain's root %q extends %q", s.Name, s.Extends)
			}
		} else if s.Extends != c.order[i-1].Name {
			t.Errorf("stage %q extends %q but follows %q in the chain",
				s.Name, s.Extends, c.order[i-1].Name)
		}
		if len(c.facts[s.Name]) == 0 {
			t.Errorf("stage %q is on the chain with no fact landing there", s.Name)
		}
	}
}

// TestStageHostPowersAreDeclaredAndClosed pins the two properties that make the
// per-stage host-power declaration a declaration rather than a convention: a
// stage cannot omit it, and it cannot name a power the host does not offer.
//
// Both refusals are shown to fire rather than assumed to. The language supplies
// neither of them — a nil slice and an empty one read alike at every site, and a
// string converted into the power type type-checks — so a refusal that silently
// stopped triggering would leave the declaration reading exactly as it does now
// while asserting nothing. The satisfied cases are asserted alongside, so a row
// cannot pass by the validator refusing everything.
func TestStageHostPowersAreDeclaredAndClosed(t *testing.T) {
	t.Parallel()

	if err := validateStageHostPowers(stages); err != nil {
		t.Fatalf("the declared stages do not satisfy their own host-power rules: %v", err)
	}
	if err := validateStageHostPowers([]stageDecl{{Name: "probe", HostPowers: noHostPowers}}); err != nil {
		t.Errorf("a stage declaring noHostPowers was refused, so the empty case cannot be stated: %v", err)
	}

	cases := []struct {
		name  string
		stage stageDecl
		want  string
	}{
		{
			name:  "an omitted declaration is refused",
			stage: stageDecl{Name: "probe"},
			want:  "omits its host-power declaration",
		},
		{
			name:  "a power the host does not offer is refused",
			stage: stageDecl{Name: "probe", HostPowers: []hostPower{hostPower("filesystem-ish")}},
			want:  "is not one the host offers",
		},
		{
			name:  "one power declared twice is refused",
			stage: stageDecl{Name: "probe", HostPowers: []hostPower{powerFilesystem, powerFilesystem}},
			want:  "twice",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateStageHostPowers([]stageDecl{tc.stage})
			if err == nil {
				t.Fatalf("the declaration %+v was admitted; the refusal is not firing, so a stage "+
					"could carry it and nothing would say so", tc.stage.HostPowers)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal names %q; want a message containing %q", err.Error(), tc.want)
			}
		})
	}
}

// TestSubjectKindsPartitionTheFacts checks the arithmetic that catches a fact
// dropped or double-counted while deriving the kind set: the per-kind tallies
// must sum to the number of declared facts.
func TestSubjectKindsPartitionTheFacts(t *testing.T) {
	t.Parallel()
	kinds, err := deriveKinds()
	if err != nil {
		t.Fatalf("the subject kinds do not derive: %v", err)
	}
	total := 0
	for _, k := range kinds {
		if len(k.Coordinates) == 0 {
			t.Errorf("kind %q has no coordinates", k.Display())
		}
		total += len(k.Facts)
	}
	if total != len(facts) {
		t.Errorf("the per-kind tallies sum to %d, %d facts are declared", total, len(facts))
	}
	t.Logf("%d facts over %d stages, keyed by %d subject kinds", len(facts), len(stages), len(kinds))
}

// TestTupleSharesAreReported checks that the near-duplicate print reports what it
// is given rather than reporting nothing.
//
// The calibration matters more than the assertion. An empty share list is the
// expected result for a registry with no near-duplicates, and an empty list is
// indistinguishable from a broken detector unless the detector is shown to fire.
// So this feeds it a pair sharing a stage, a carrier and a subject, and requires
// the pair back.
func TestTupleSharesAreReported(t *testing.T) {
	t.Parallel()
	for _, s := range deriveTupleShares(facts) {
		t.Logf("shared tuple (%s, %s, %s): %v", s.Stage, s.SourceField, s.Subject, s.Facts)
	}

	probe := facts[0]
	probe.Name = probe.Name + "-probe"
	probed := append(append([]factDecl(nil), facts...), probe)

	var found bool
	for _, s := range deriveTupleShares(probed) {
		if len(s.Facts) == 2 && s.Facts[0] == facts[0].Name && s.Facts[1] == probe.Name {
			found = true
		}
	}
	if !found {
		t.Errorf("two facts sharing a stage, a source carrier and a subject were not reported as sharing "+
			"a tuple; the near-duplicate print is not firing, so an empty share list would mean nothing.\n"+
			"got: %+v", deriveTupleShares(probed))
	}
}

// authoredSlotCarrier is the structure whose authored fields the facts below
// must account for: one attachment slot as an entity declares it.
const authoredSlotCarrier = "types.AttachmentSlot"

// authoredSocketCarrier is the second declaration held in lockstep: one
// dependency socket as an entity declares it.
const authoredSocketCarrier = "types.UsesSchemaEntry"

// attachmentSlotFieldsWithoutFacts records, per authored field of the slot
// declaration, why no fact reads it. An entry is a claim that no rule over the
// slot's shape has an input there, and the pairing below refuses one that has
// gone stale — a field the declaration stopped carrying, or one a fact has since
// been declared for.
var attachmentSlotFieldsWithoutFacts = map[string]string{
	"Description": "prose addressed to whoever reads the declaration. The " +
		"validation surface projected from a slot is built from the slot's other " +
		"fields and consults this one nowhere, so a rule over the slot's shape has " +
		"no input here and a fact carrying it would be read by nothing",
}

// authoredCarriersHeldInLockstep are the declarations whose every authored field
// must be accounted for — by a fact that reads it, or by a recorded reason why
// no rule has an input there.
//
// It is a list rather than one entry because the failure it prevents is a
// property of the class rather than of any one declaration: an authored field no
// fact reads is invisible to every rule over that declaration's shape, whichever
// declaration it sits on. A carrier absent from this list is unguarded, so
// adding one here is how the guarantee widens.
var authoredCarriersHeldInLockstep = []struct {
	carrier string
	decl    reflect.Type
	// without records, per authored field, why no fact reads it. Empty means
	// every authored field the declaration carries is read by some fact, which
	// is an assertion rather than an omission: the pairing refuses a reason that
	// has gone stale, so an entry added later has to earn its place.
	without map[string]string
}{
	{
		carrier: authoredSlotCarrier,
		decl:    reflect.TypeOf(types.AttachmentSlot{}),
		without: attachmentSlotFieldsWithoutFacts,
	},
	{
		carrier: authoredSocketCarrier,
		decl:    reflect.TypeOf(types.UsesSchemaEntry{}),
		without: nil,
	},
}

// slotFieldFindings is what the pairing reports over one declaration: authored
// fields nothing accounts for, facts naming a field the declaration does not
// carry, and recorded reasons that no longer hold.
type slotFieldFindings struct {
	unexplained []string
	dangling    []string
	stale       []string
}

// pairAuthoredFieldsWithFacts partitions a declaration's authored field set
// against the facts declared over it and the reasons recorded for the rest.
//
// A field is authored when it is exported and carries a yaml key, because the
// key is the name someone writes in the document; a field excluded from
// serialization is correctly outside the pairing.
//
// It is one implementation rather than one per caller. The calibration below
// feeds it a declaration carrying a field nothing covers, and a calibration
// running its own copy of this partition would only prove that copy fires.
func pairAuthoredFieldsWithFacts(
	decl reflect.Type,
	carrier string,
	decls []factDecl,
	without map[string]string,
) slotFieldFindings {
	authored := map[string]bool{}
	for i := 0; i < decl.NumField(); i++ {
		f := decl.Field(i)
		if !f.IsExported() {
			continue
		}
		key, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if key == "" || key == "-" {
			continue
		}
		authored[f.Name] = true
	}

	covered := map[string]string{}
	for _, f := range decls {
		if field, ok := strings.CutPrefix(f.SourceField, carrier+"."); ok {
			covered[field] = f.Name
		}
	}

	var out slotFieldFindings
	for name := range authored {
		if covered[name] == "" && without[name] == "" {
			out.unexplained = append(out.unexplained, name)
		}
	}
	for field := range covered {
		if !authored[field] {
			out.dangling = append(out.dangling, field)
		}
	}
	for field, why := range without {
		switch {
		case why == "":
			out.stale = append(out.stale, field+" (recorded with no reason given)")
		case !authored[field]:
			out.stale = append(out.stale, field+" (the declaration no longer carries this authored field)")
		case covered[field] != "":
			out.stale = append(out.stale, field+" (fact "+covered[field]+" now reads it)")
		}
	}

	sort.Strings(out.unexplained)
	sort.Strings(out.dangling)
	sort.Strings(out.stale)
	return out
}

// TestEveryAuthoredCarrierFieldHasAFact holds each registered declaration and
// the facts over it in lockstep, in both directions.
//
// The failure it prevents is silent, which is why it is a test rather than a
// convention. A rule over a declaration's shape reads facts; an authored field no
// fact reads is simply never looked at, so the rule still runs, still passes, and
// reports nothing about that field. Nothing goes red, so nothing prompts anyone
// to look — and a census someone has to remember to re-run is not enforcement.
//
// Both directions are asserted per carrier. An authored field with neither a fact
// nor a recorded reason fails, and so does a fact naming a field the declaration
// does not carry, which is what a rename leaves behind.
//
// The carrier set is asserted non-empty for the same reason each carrier's field
// set is: a loop over no rows passes every assertion inside it.
func TestEveryAuthoredCarrierFieldHasAFact(t *testing.T) {
	t.Parallel()

	if len(authoredCarriersHeldInLockstep) == 0 {
		t.Fatal("no carrier is held in lockstep, so this test checks nothing")
	}

	for _, c := range authoredCarriersHeldInLockstep {
		t.Run(c.carrier, func(t *testing.T) {
			t.Parallel()

			found := pairAuthoredFieldsWithFacts(c.decl, c.carrier, facts, c.without)

			// A declaration reporting no authored fields would satisfy every
			// assertion below while checking nothing, so the population is
			// asserted before the findings are read.
			empty := pairAuthoredFieldsWithFacts(c.decl, c.carrier, nil, nil)
			if len(empty.unexplained) == 0 {
				t.Fatalf("%s reports no authored fields at all, so this pairing checks nothing",
					c.carrier)
			}
			t.Logf("%s carries %d authored field(s)", c.carrier, len(empty.unexplained))

			if len(found.unexplained) > 0 {
				t.Errorf("%s declares %d authored field(s) that no fact reads and no recorded "+
					"reason covers: %v\neach is either a field a rule over this declaration's "+
					"shape cannot see, which wants a fact, or one no rule has a use for, which "+
					"wants its reason recorded beside the others",
					c.carrier, len(found.unexplained), found.unexplained)
			}
			if len(found.dangling) > 0 {
				t.Errorf("%d fact(s) read a field %s does not carry as an authored field: %v\n"+
					"a fact naming a field that was renamed or removed reads nothing",
					len(found.dangling), c.carrier, found.dangling)
			}
			if len(found.stale) > 0 {
				t.Errorf("%d recorded reason(s) no longer hold over %s: %v\nremove each entry",
					len(found.stale), c.carrier, found.stale)
			}
		})
	}
}

// TestTheAttachmentSlotPairingReportsWhatItIsGiven calibrates the pairing above.
//
// The calibration matters more than the assertion. Over a declaration whose
// every field is accounted for the pairing reports nothing, and an empty finding
// is indistinguishable from a partition that inspects nothing unless it is shown
// to fire. So each row feeds it a declaration it must object to, and requires
// the objection back.
func TestTheAttachmentSlotPairingReportsWhatItIsGiven(t *testing.T) {
	t.Parallel()

	// Each probe stands in for the slot declaration. The yaml keys are what make
	// a field authored, so they are spelled out rather than left off.
	type uncoveredField struct {
		Required bool   `yaml:"required,omitempty"`
		Cadence  string `yaml:"cadence,omitempty"`
	}
	type renamedAway struct {
		Cadence string `yaml:"cadence,omitempty"`
	}
	type unauthoredField struct {
		Required bool   `yaml:"required,omitempty"`
		Internal string `yaml:"-"`
	}

	cases := []struct {
		name    string
		decl    reflect.Type
		without map[string]string
		read    func(slotFieldFindings) []string
		want    string
	}{
		{
			name: "an authored field no fact reads is reported",
			decl: reflect.TypeOf(uncoveredField{}),
			read: func(f slotFieldFindings) []string { return f.unexplained },
			want: "Cadence",
		},
		{
			name: "a fact reading a field the declaration dropped is reported",
			decl: reflect.TypeOf(renamedAway{}),
			read: func(f slotFieldFindings) []string { return f.dangling },
			want: "Required",
		},
		{
			name:    "a reason recorded for a field the declaration does not carry is reported",
			decl:    reflect.TypeOf(uncoveredField{}),
			without: map[string]string{"Retired": "a reason for a field that is gone"},
			read:    func(f slotFieldFindings) []string { return f.stale },
			want:    "Retired",
		},
		{
			name:    "a reason recorded for a field a fact now reads is reported",
			decl:    reflect.TypeOf(uncoveredField{}),
			without: map[string]string{"Required": "a reason a fact has overtaken"},
			read:    func(f slotFieldFindings) []string { return f.stale },
			want:    "Required",
		},
		{
			name:    "a reason recorded with no reason given is reported",
			decl:    reflect.TypeOf(uncoveredField{}),
			without: map[string]string{"Cadence": ""},
			read:    func(f slotFieldFindings) []string { return f.stale },
			want:    "Cadence",
		},
		{
			name: "a field excluded from the document is not demanded",
			decl: reflect.TypeOf(unauthoredField{}),
			read: func(f slotFieldFindings) []string { return f.unexplained },
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := pairAuthoredFieldsWithFacts(tc.decl, authoredSlotCarrier, facts, tc.without)
			reported := tc.read(got)
			if tc.want == "" {
				if len(reported) > 0 {
					t.Errorf("the pairing objected to %v; nothing was owed here, so it is "+
						"reporting a field that is not authored", reported)
				}
				return
			}
			for _, r := range reported {
				if strings.HasPrefix(r, tc.want) {
					return
				}
			}
			t.Errorf("the pairing did not name %q; it is not firing, so an empty finding over the "+
				"real declaration would mean nothing.\ngot: %+v", tc.want, got)
		})
	}
}

// TestInvocationReadClassifiesWhatItIsGiven exercises the syntactic half of the
// invocation read against sources written here, so each classification is shown to
// fire rather than inferred from a clean result over the real tree.
//
// Every question the read answers over the real tree is a search for something
// that may legitimately be absent, and the tree currently answers "nothing is
// invoked". A read that classified nothing would produce exactly that answer, so
// the rows below are the needles: each one MUST be found, and a row finding
// nothing fails.
func TestInvocationReadClassifiesWhatItIsGiven(t *testing.T) {
	t.Parallel()

	entries := map[string]bool{"RunOnNodes": true, "RunOnEdges": true}

	// The rows assert what the syntactic reads return, which is deliberately
	// blind to whether a call is a production invocation: the framework-versus-
	// production split is made by the caller, from the directory the file sits
	// in, and is asserted over the real tree below. A row therefore lists the
	// calls found, not the calls that would count.
	for _, tc := range []struct {
		name string
		src  string

		wantChecks []declaredCheck
		wantSites  []invocationSite
	}{
		{
			name: "a qualified declaration is read with its view and subject",
			src: `package p
import "x/constraint"
var Rule = constraint.Check[constraint.NormalizedView, constraint.NodeSubject]{}
`,
			wantChecks: []declaredCheck{{
				Name: "Rule", Package: "d", View: "NormalizedView", Subject: "NodeSubject",
			}},
		},
		{
			name: "the framework's own unqualified declaration is read too",
			src: `package constraint
var Rule = Check[GraphView, EdgeSubject]{}
`,
			wantChecks: []declaredCheck{{
				Name: "Rule", Package: "d", View: "GraphView", Subject: "EdgeSubject",
			}},
		},
		{
			name: "a composite literal that is not a check is ignored",
			src: `package p
type other[A any, B any] struct{}
var NotARule = other[int, string]{}
`,
		},
		{
			name: "a qualified call naming its check is attributed",
			src: `package p
import "x/constraint"
func run() { _, _ = constraint.RunOnNodes(nil, nil, nil, Rule) }
`,
			wantSites: []invocationSite{{Entry: "RunOnNodes", File: "d/f.go", Check: "Rule"}},
		},
		{
			name: "a call naming a check from another package is attributed to the check's own name",
			src: `package p
import "x/constraint"
func run() { _, _ = constraint.RunOnEdges(nil, nil, nil, analyzer.Rule) }
`,
			wantSites: []invocationSite{{Entry: "RunOnEdges", File: "d/f.go", Check: "Rule"}},
		},
		{
			name: "a call whose check arrives through a parameter is a site with no check attributed",
			src: `package p
import "x/constraint"
func run(c any) { _, _ = constraint.RunOnNodes(nil, nil, nil, checksFor(c)) }
`,
			wantSites: []invocationSite{{Entry: "RunOnNodes", File: "d/f.go", Check: ""}},
		},
		{
			name: "an unqualified call is seen, which is how an in-framework call is seen at all",
			src: `package constraint
func run() { _, _ = RunOnNodes(nil, nil, nil, Rule) }
`,
			wantSites: []invocationSite{{Entry: "RunOnNodes", File: "d/f.go", Check: "Rule"}},
		},
		{
			name: "a call to something else entirely is not a site",
			src: `package p
func run() { _ = doSomething(nil) }
`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "d/f.go", tc.src, 0)
			if err != nil {
				t.Fatalf("parse the fixture: %v", err)
			}

			gotChecks := declaredChecksIn("d", file)
			if !reflect.DeepEqual(gotChecks, tc.wantChecks) {
				t.Errorf("declared checks:\n got %+v\nwant %+v", gotChecks, tc.wantChecks)
			}

			gotSites := traversalCallSites(fset, "", file, entries)
			if !reflect.DeepEqual(gotSites, tc.wantSites) {
				t.Errorf("production sites:\n got %+v\nwant %+v", gotSites, tc.wantSites)
			}

			if got := countTraversalCalls(file, entries); got != len(tc.wantSites) {
				t.Errorf("traversal calls counted = %d, want %d; the count and the site list read the "+
					"same calls, so they cannot disagree", got, len(tc.wantSites))
			}
		})
	}
}

// TestInvocationReadReachesTheTree asserts the read over the real tree is a
// measurement rather than an empty instrument, and that what it attributes is
// internally consistent.
//
// The read's own refusals cover the three vacuity shapes — no file parsed, no
// traversal entry found, no check declared — so a nil error here is already the
// assertion that the corpus was reached. What this adds is the two consistency
// properties a clean-but-wrong read would violate: a check the placement chain
// cannot place, and a site attributed to a name no declaration carries.
func TestInvocationReadReachesTheTree(t *testing.T) {
	t.Parallel()

	state, err := readInvocationState()
	if err != nil {
		t.Fatalf("the invocation read did not complete, so the placement table cannot state which "+
			"checks are invoked: %v", err)
	}

	c, err := deriveChain()
	if err != nil {
		t.Fatalf("derive the stage chain: %v", err)
	}
	placeable := map[string]bool{}
	for _, s := range c.order {
		placeable[viewName(s.Name)] = true
	}

	declared := map[string]bool{}
	for _, ck := range state.Checks {
		declared[ck.Name] = true
		if !placeable[ck.View] {
			t.Errorf("check %q in %s declares view %q, which the stage chain does not order, so the "+
				"placement table can print no stage for it", ck.Name, ck.Package, ck.View)
		}
	}

	for _, site := range state.Sites {
		if site.Check != "" && !declared[site.Check] {
			t.Errorf("the site in %s names %q, which is not a declared check; the read has attributed "+
				"an invocation to something it cannot place", site.File, site.Check)
		}
	}

	if state.InvokedCount()+state.DeclaredOnly() != len(state.Checks) {
		t.Errorf("the invoked and declared-only counts (%d + %d) do not partition the %d declared "+
			"checks, so the table's totals would not add up",
			state.InvokedCount(), state.DeclaredOnly(), len(state.Checks))
	}
}
