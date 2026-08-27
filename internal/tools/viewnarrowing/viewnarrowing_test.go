// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package viewnarrowing

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// constraintPackage is where the view chain is declared. These tests read the
// real chain rather than a synthetic one, so a fact moving to another stage moves
// what they expect in the same edit that moves the fact.
const constraintPackage = "../../compiler/constraint"

const checkCorpus = "testdata/checks"

// TestReadChainDerivesItFromTheDeclaredViews reads the shipped chain and pins
// what was derived from it: the base, the stages in order, and the stage each
// fact becomes readable at.
//
// The expectations are deliberately spot-checks against the source rather than a
// copy of it. A test restating every fact would be the hand-written table this
// package exists not to have, one file further out.
func TestReadChainDerivesItFromTheDeclaredViews(t *testing.T) {
	t.Parallel()

	chain, err := ReadChain(constraintPackage)
	if err != nil {
		t.Fatalf("read the view chain: %v", err)
	}

	if got, want := chain.Base(), "View"; got != want {
		t.Errorf("the base of the chain reads as %q; want %q — the base is taken from the check type's "+
			"first type-parameter constraint, so a wrong one means the chain was assembled from the "+
			"wrong root", got, want)
	}

	wantStages := []string{
		"DocumentView",
		"NormalizedView",
		"TopologyView",
		"GraphView",
		"DescribedView",
		"HydratedView",
		"DispatchedView",
		"RenderedView",
	}
	var gotStages []string
	for _, stage := range chain.Stages() {
		gotStages = append(gotStages, stage.Name)
	}
	if !slices.Equal(gotStages, wantStages) {
		t.Fatalf("the chain reads as %v; want %v — order is the whole of a placement, so a wrong order "+
			"names a wrong narrower view for every check", gotStages, wantStages)
	}

	// Facts placed by reading the embedding. Each is expected readable from its
	// own stage outward and NOT one stage earlier; the pair is what makes this a
	// placement rather than a membership test.
	for _, tc := range []struct {
		fact  string
		stage string
	}{
		{"DeclaredParameterKeys", "DocumentView"},
		{"SocketRequired", "NormalizedView"},
		{"ResolvedParameter", "TopologyView"},
		{"PerLeafAttachmentProvenance", "GraphView"},
		{"StepExportScope", "DescribedView"},
		{"DeclaredLocalsKeys", "HydratedView"},
		{"AssemblyGroupProviderSet", "DispatchedView"},
		{"EmittedFilePathSet", "RenderedView"},
	} {
		at, ok := chain.Index(tc.stage)
		if !ok {
			t.Fatalf("the chain carries no stage named %s", tc.stage)
		}
		if !slices.Contains(chain.FactsAvailableAt(at), tc.fact) {
			t.Errorf("%s is not readable at %s, though that is the stage declaring it", tc.fact, tc.stage)
		}
		if at > 0 && slices.Contains(chain.FactsAvailableAt(at-1), tc.fact) {
			t.Errorf("%s is readable one stage before %s, so the chain places it earlier than its "+
				"declaration does and every check reading it would be reported as over-declaring",
				tc.fact, tc.stage)
		}
	}

	// Calibration. A reader answering "readable" for anything would place every
	// check at the innermost stage and report the whole corpus as over-declaring;
	// one answering "unknown" for everything would report nothing at all. Require
	// both answers on known cases before believing the verdicts above.
	if _, unknown := chain.NarrowestSufficient([]string{"NoSuchFactIsDeclaredAnywhere"}); len(unknown) != 1 {
		t.Errorf("a fact no view declares is not reported as unknown, so the reader answers "+
			"\"readable\" for anything (unknown: %v)", unknown)
	}
	if _, unknown := chain.NarrowestSufficient([]string{"SocketRequired"}); len(unknown) != 0 {
		t.Errorf("a fact a view does declare is reported as unknown, so the reader answers "+
			"\"unknown\" for everything (unknown: %v)", unknown)
	}
}

// TestTheChainIsReadAndNotTabulated asserts that this package holds no copy of
// what it reads.
//
// A hand-written accessor-to-stage table would agree with the chain on the day
// it was written and drift silently afterwards: the chain is generated from the
// fact registry, so a fact moving stages would leave the table confidently wrong
// rather than failing. So no view name and no fact name may appear anywhere in
// this package's own non-test source, as an identifier or inside a string.
func TestTheChainIsReadAndNotTabulated(t *testing.T) {
	t.Parallel()

	chain, err := ReadChain(constraintPackage)
	if err != nil {
		t.Fatalf("read the view chain: %v", err)
	}

	forbidden := []string{chain.Base()}
	for _, stage := range chain.Stages() {
		forbidden = append(forbidden, stage.Name)
		forbidden = append(forbidden, stage.Facts...)
	}
	if len(forbidden) < 7 {
		t.Fatalf("only %d names were read from the chain, so this assertion would search for almost "+
			"nothing and pass over anything", len(forbidden))
	}

	tokens := packageTokens(t, ".")

	// Calibration, both directions. The search must find a token this package
	// does contain, and must not find one it does not.
	if !tokens[checkTypeName] {
		t.Fatalf("the search does not see %q in this package's own source, though it is declared there, "+
			"so it is not a working instrument (saw %d tokens)", checkTypeName, len(tokens))
	}
	if tokens["NoSuchTokenIsWrittenHere"] {
		t.Fatalf("the search claims to see a token written nowhere, so it would report every name as " +
			"present")
	}

	for _, name := range forbidden {
		if tokens[name] {
			t.Errorf("this package's own source names %q, which is a name read from the view chain.\n"+
				"INVARIANT: the chain is read from the source declaring it, never restated here.\n"+
				"CONSEQUENCE: a restated name agrees with the chain today and drifts the moment a fact "+
				"moves stages — and a wrong placement names a confident wrong narrower view rather than "+
				"failing.", name)
		}
	}
}

// TestAnalyzeReportsOverDeclarationAndClearsTheExactDeclaration is the acceptance
// assertion: the reading reports a check declaring a wider view than its body
// reads, and names the narrower view that would suffice.
//
// The corpus carries a correctly-declaring check for a reason that is not
// symmetry. Without it, a reading that flagged every check it saw would satisfy
// the over-declaration expectation while proving nothing at all. The control is
// what makes the positive result mean something.
func TestAnalyzeReportsOverDeclarationAndClearsTheExactDeclaration(t *testing.T) {
	t.Parallel()

	chain, err := ReadChain(constraintPackage)
	if err != nil {
		t.Fatalf("read the view chain: %v", err)
	}

	findings, err := Analyze(chain, checkCorpus)
	if err != nil {
		t.Fatalf("read the check corpus: %v", err)
	}

	type expectation struct {
		check     string
		kind      Kind
		declared  string
		narrowest string
	}
	// In report order: by file, then by position within the file.
	want := []expectation{
		{check: "OverDeclaringSocketRule", kind: OverDeclared, declared: "GraphView", narrowest: "NormalizedView"},
		{check: "AccessorThroughAVariableRule", kind: OverDeclared, declared: "GraphView", narrowest: "NormalizedView"},
		{check: "LocallyDeclaredViewRule", kind: UnknownView, declared: "localView"},
		{check: "MisspelledFactRule", kind: UnknownFact, declared: "RenderedView"},
		{check: "ViewHandedToAHelperRule", kind: Unreadable, declared: "HydratedView"},
		{check: "SubjectOnlyRule", kind: NoFactRead, declared: "GraphView", narrowest: "DocumentView"},
		{check: "PredicateElsewhereRule", kind: Unreadable, declared: "HydratedView"},
	}

	var got []expectation
	for _, finding := range findings {
		got = append(got, expectation{
			check:     finding.Check,
			kind:      finding.Kind,
			declared:  finding.DeclaredView,
			narrowest: finding.NarrowestView,
		})
	}

	if len(got) != len(want) {
		t.Fatalf("the corpus reports %d findings; want %d.\nreport:\n%s", len(got), len(want), Report(findings))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("finding %d reads %+v; want %+v", i, got[i], want[i])
		}
	}

	// The control, stated as its own failure so that its role cannot be missed.
	for _, finding := range findings {
		if finding.Check == "ExactlyDeclaredParameterRule" {
			t.Errorf("the control check ExactlyDeclaredParameterRule is reported as %q. It declares the "+
				"narrowest view that suffices, so reporting it means the reading flags checks rather "+
				"than reading them — and the over-declaration result above then proves nothing, because "+
				"a reading that flags everything satisfies it.\n%s", finding.Kind, finding)
		}
	}
}

// TestTheVerdictTracksTheDeclarationAndNotTheFile is the calibration that makes
// the acceptance assertion above mean something.
//
// One body, two declarations, in a corpus written outside this package's own
// testdata. The wider declaration must be reported and the narrower one must be
// silent — if the report followed the body alone it would fire on both, and if it
// followed nothing it would fire on neither. Either failure looks exactly like a
// pass when only the over-declaring case is tried.
func TestTheVerdictTracksTheDeclarationAndNotTheFile(t *testing.T) {
	t.Parallel()

	// Two facts, both complete at the second stage of the chain, read the same way
	// in both variants. The component type is read as a fact keyed by the node
	// rather than taken from the subject, because a subject carries the
	// coordinates its kind is keyed by and a component type is not one of them.
	const corpus = `package probe

import "github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"

var Rule = constraint.Check[constraint.%[1]s, constraint.NodeSocketSubject]{
	Run: func(v constraint.%[1]s, s constraint.NodeSocketSubject) []constraint.Objection {
		componentType, declared := v.InstanceComponentType(s.NodeID())
		if !declared {
			return nil
		}
		if v.SocketRequired(componentType, s.SocketName()) {
			return nil
		}
		return []constraint.Objection{s.Raise(nil, s.SocketName())}
	},
}
`

	chain, err := ReadChain(constraintPackage)
	if err != nil {
		t.Fatalf("read the view chain: %v", err)
	}

	for _, tc := range []struct {
		declared string
		want     []Kind
	}{
		{declared: "RenderedView", want: []Kind{OverDeclared}},
		{declared: "GraphView", want: []Kind{OverDeclared}},
		{declared: "NormalizedView", want: nil},
	} {
		t.Run(tc.declared, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			source := fmt.Sprintf(corpus, tc.declared)
			if err := os.WriteFile(filepath.Join(dir, "probe.go"), []byte(source), 0o600); err != nil {
				t.Fatalf("write the probe corpus: %v", err)
			}

			findings, err := Analyze(chain, dir)
			if err != nil {
				t.Fatalf("read the probe corpus: %v", err)
			}

			var got []Kind
			for _, finding := range findings {
				got = append(got, finding.Kind)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("declaring %s over a body reading second-stage facts reports %v; want %v.\n%s",
					tc.declared, got, tc.want, Report(findings))
			}
			for _, finding := range findings {
				if finding.NarrowestView != "NormalizedView" {
					t.Errorf("the narrower view is named as %q; want NormalizedView, which is the stage "+
						"declaring every fact the body reads", finding.NarrowestView)
				}
			}
		})
	}
}

// TestTheOverDeclarationReportNamesTheNarrowerView pins the text, because naming
// the narrower view is half of what the report is for: a finding saying only
// "too wide" leaves the reader to re-derive the answer the reading already has.
func TestTheOverDeclarationReportNamesTheNarrowerView(t *testing.T) {
	t.Parallel()

	chain, err := ReadChain(constraintPackage)
	if err != nil {
		t.Fatalf("read the view chain: %v", err)
	}
	findings, err := Analyze(chain, checkCorpus)
	if err != nil {
		t.Fatalf("read the check corpus: %v", err)
	}

	var checked int
	for _, finding := range findings {
		if finding.Kind != OverDeclared {
			continue
		}
		checked++
		line := finding.String()
		for _, want := range []string{finding.DeclaredView, finding.NarrowestView, string(OverDeclared)} {
			if !strings.Contains(line, want) {
				t.Errorf("the finding line %q does not name %q", line, want)
			}
		}
		if !strings.Contains(line, "declare "+finding.NarrowestView) {
			t.Errorf("the finding line %q does not say what to declare instead", line)
		}
	}
	if checked == 0 {
		t.Fatalf("no over-declaration was reported, so this assertion inspected nothing")
	}
}

// TestTheReportIsByteIdenticalAcrossRuns pins the ordering. Findings are
// assembled from parsed maps, so an order left to map iteration would vary
// between runs, and a report nobody can diff is a report nobody reads.
func TestTheReportIsByteIdenticalAcrossRuns(t *testing.T) {
	t.Parallel()

	chain, err := ReadChain(constraintPackage)
	if err != nil {
		t.Fatalf("read the view chain: %v", err)
	}

	first, err := Analyze(chain, checkCorpus)
	if err != nil {
		t.Fatalf("read the check corpus: %v", err)
	}
	if len(first) == 0 {
		t.Fatalf("the corpus reports nothing, so comparing two empty reports would certify nothing")
	}
	want := Report(first)

	for range 8 {
		reread, err := ReadChain(constraintPackage)
		if err != nil {
			t.Fatalf("read the view chain: %v", err)
		}
		again, err := Analyze(reread, checkCorpus)
		if err != nil {
			t.Fatalf("read the check corpus: %v", err)
		}
		if got := Report(again); got != want {
			t.Fatalf("the report differs between runs over identical input:\nfirst:\n%s\nagain:\n%s", want, got)
		}
	}
}

// TestReadChainRefusesAChainThatIsNotOne pins the refusal. A shape with no
// single order between its stages has no narrowest sufficient view to compute, so
// the answer is an error rather than a pick.
func TestReadChainRefusesAChainThatIsNotOne(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		dir  string
		want string
	}{
		{
			name: "two views embed the base",
			dir:  "testdata/stages-branch-out",
			want: "View is embedded by AlphaView and BetaView",
		},
		{
			name: "one view embeds two",
			dir:  "testdata/stages-branch-in",
			want: "JoinView embeds AlphaView and BetaView",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := ReadChain(tc.dir); err == nil {
				t.Fatalf("reading %s returned a chain; want a refusal — a shape with no order between "+
					"its stages would otherwise yield an arbitrary narrower view for every check reading "+
					"across it", tc.dir)
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal reads %q; want it to name %q, so the reader is told which two "+
					"stages have no order between them", err.Error(), tc.want)
			}
		})
	}
}

// ExampleAnalyze shows what the reading says about the fixture corpus.
//
// It prints the kind and the two views rather than the whole finding line, so
// that the record of what each shape reports survives an edit to a fixture's
// comments — the file and line belong in a report a reader is looking at, not in
// an expectation.
func ExampleAnalyze() {
	chain, err := ReadChain(constraintPackage)
	if err != nil {
		fmt.Println(err)
		return
	}
	findings, err := Analyze(chain, checkCorpus)
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, finding := range findings {
		narrowest := finding.NarrowestView
		if narrowest == "" {
			narrowest = "(none computed)"
		}
		fmt.Printf("%-14s %-28s declares %-14s narrowest %s\n",
			finding.Kind, finding.Check, finding.DeclaredView, narrowest)
	}
	// Output:
	// over-declared  OverDeclaringSocketRule      declares GraphView      narrowest NormalizedView
	// over-declared  AccessorThroughAVariableRule declares GraphView      narrowest NormalizedView
	// unknown-view   LocallyDeclaredViewRule      declares localView      narrowest (none computed)
	// unknown-fact   MisspelledFactRule           declares RenderedView   narrowest (none computed)
	// unreadable     ViewHandedToAHelperRule      declares HydratedView   narrowest (none computed)
	// no-fact-read   SubjectOnlyRule              declares GraphView      narrowest DocumentView
	// unreadable     PredicateElsewhereRule       declares HydratedView   narrowest (none computed)
}

// packageTokens are the identifiers, and the words inside the string and comment
// text, of a package's non-test sources.
//
// It reads tokens rather than raw substrings deliberately: a substring search for
// the base view's name matches every field and constant whose own name ends in
// it, which would make the assertion it feeds fail over correct code.
func packageTokens(t *testing.T, dir string) map[string]bool {
	t.Helper()

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", dir, err)
	}

	out := map[string]bool{}
	words := func(text string) {
		for _, word := range strings.FieldsFunc(text, func(r rune) bool {
			return !(r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
		}) {
			out[word] = true
		}
	}
	var files int
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			files++
			ast.Inspect(file, func(node ast.Node) bool {
				switch n := node.(type) {
				case *ast.Ident:
					out[n.Name] = true
				case *ast.BasicLit:
					words(n.Value)
				}
				return true
			})
			for _, group := range file.Comments {
				words(group.Text())
			}
		}
	}
	if files == 0 {
		t.Fatalf("no non-test source was read from %s, so the search would see nothing", dir)
	}
	return out
}
