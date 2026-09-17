// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// The proofs in this file cover the condition raised when an Aggregate's
// authored text is not a Go template. They compile the fixture workspace for
// real, because what is under proof is that a build halts through the rule and
// what the halt then names — neither of which a direct call to a predicate can
// show.
//
// One document a build starts from is refused by the declared rule, at the point
// the file is in hand. A sub-Aggregate the composition imports is refused by the
// raise inside the render, because no authored document is presented for it. The
// two are proved separately here, and the second is the one that would go silent
// if the raise were ever removed alongside the rule.

// unterminatedAction is the plant: an action opened and never closed, which the
// template parser refuses wherever it sits.
//
// It is planted in a comment line rather than in a value, and the reason is that
// the fault under proof is a property of the file's TEXT rather than of any key
// in it — the whole document is rendered as one template before anything reads it
// as YAML. Planting it in a value would additionally put an unexpected key into a
// component's configuration, so a control that then compiled would be a control
// for that key as much as for this condition.
const unterminatedAction = "# {{ .parameters.region \n"

// faultedRootAggregate is the committed fixture Aggregate with the plant appended
// and nothing else changed, so the faulted document and the control differ in one
// line. Reading the committed bytes rather than restating them is what keeps the
// pair from drifting apart as the fixture moves.
func faultedRootAggregate(t *testing.T) (faulted, control []byte) {
	t.Helper()

	control, err := os.ReadFile(invocationAggregatePath)
	if err != nil {
		t.Fatalf("read the fixture Aggregate: %v", err)
	}
	return append(append([]byte{}, control...), []byte(unterminatedAction)...), control
}

// TestAggregateTextThatIsNotATemplateHaltsTheBuild is the invocation-and-content
// proof: a real compilation of a document an author can write, halting through
// the rule, with the condition, its short code and the document the finding names
// all read off the halt itself.
func TestAggregateTextThatIsNotATemplateHaltsTheBuild(t *testing.T) {
	t.Parallel()

	faulted, _ := faultedRootAggregate(t)
	_, err := compileAggregateBytes(t, string(faulted))
	if err == nil {
		t.Fatal("an Aggregate whose text is not a template compiled without halting")
	}
	if !errors.Is(err, resolver.ErrInvalidParameterRef()) {
		t.Fatalf("the halt does not carry the invalid-parameter-reference condition: %v", err)
	}
	short, ok := cerrors.ShortCodeOf(err)
	if !ok || short != "E0158" {
		t.Errorf("short code = %q (present %t), want %q", short, ok, "E0158")
	}

	var finding *resolver.ResolverError
	if !errors.As(err, &finding) {
		t.Fatalf("the halt must carry the structured finding; got %T: %v", err, err)
	}
	// The document the finding names is the file, supplied by the framework from
	// the presented document rather than written by the rule. Before this rule
	// existed the same fault named the kind word alone, so an author was told which
	// kind of document was at fault and never which one.
	if !strings.Contains(finding.Entity, invocationAggregatePath) {
		t.Errorf("entity reference = %q, want it to name %q — a finding naming only the kind "+
			"leaves the author without the file", finding.Entity, invocationAggregatePath)
	}
	// The subject is the document, so there is no authored element inside it to
	// position at. Asserted because an empty locator here is the rule's declared
	// shape rather than a locator that went missing.
	if finding.Field != "" {
		t.Errorf("locator = %q, want empty: the fault belongs to the file rather than to a key",
			finding.Field)
	}
	rendered := finding.Error()
	for _, want := range []string{
		"invalid parameter reference",
		invocationAggregatePath,
		"template parse error",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the rendered finding does not carry %q: %q", want, rendered)
		}
	}
	t.Logf("rendered finding: %s", rendered)
	// The remedy is the developer's own document, so the category has to send them
	// there rather than to the catalog.
	if got := finding.ToCompilerError().Category(); got != cerrors.DeveloperError {
		t.Errorf("category = %v, want %v", got, cerrors.DeveloperError)
	}
}

// TestAggregateTextThatIsATemplateCompiles is the control. Without it the proof
// above would pass on a rule that refused every document, and the faulted
// document differs from this one in exactly the planted line.
func TestAggregateTextThatIsATemplateCompiles(t *testing.T) {
	t.Parallel()

	_, control := faultedRootAggregate(t)
	if _, err := compileAggregateBytes(t, string(control)); err != nil {
		t.Fatalf("the fixture Aggregate halted without the plant: %v", err)
	}
}

// TestTheRootHaltIsReportedOnceAndByTheRule is the disjointness proof, and it is
// what licenses the raise inside the render staying where it is.
//
// The same fault has two paths that could report it, and the question is whether
// one input ever reaches both. It does not: the rule halts the build before the
// render is reached, so the render's raise cannot also fire for this document.
// The A/B is what makes that observable — the render refuses the same bytes when
// asked directly, so it would have reported this fault had the rule not halted
// first, and the halt the build produced names the file, which only the rule
// does.
func TestTheRootHaltIsReportedOnceAndByTheRule(t *testing.T) {
	t.Parallel()

	faulted, _ := faultedRootAggregate(t)

	// A: asked directly, the render refuses these bytes — so the raise inside it is
	// live and would have reported this fault.
	_, renderErr := resolver.InterpolateParameters(faulted, map[string]any{})
	if renderErr == nil {
		t.Fatal("the render accepted the planted text, so this pair proves nothing about which " +
			"path reported the build's halt")
	}
	var renderFinding *resolver.ResolverError
	if !errors.As(renderErr, &renderFinding) {
		t.Fatalf("the render's refusal must carry the structured finding; got %T", renderErr)
	}
	if strings.Contains(renderFinding.Entity, invocationAggregatePath) {
		t.Fatal("the render's own refusal names the file, so naming the file no longer " +
			"distinguishes which path reported a halt and this proof has lost its discriminator")
	}

	// B: compiled for real, exactly one finding carries the condition, and it names
	// the file — which the render's raise, asserted above, does not.
	_, err := compileAggregateBytes(t, string(faulted))
	if err == nil {
		t.Fatal("the faulted Aggregate compiled without halting")
	}
	var finding *resolver.ResolverError
	if !errors.As(err, &finding) {
		t.Fatalf("the halt must carry the structured finding; got %T: %v", err, err)
	}
	if !strings.Contains(finding.Entity, invocationAggregatePath) {
		t.Errorf("the build's halt names %q, so it came from the render's raise rather than "+
			"from the rule", finding.Entity)
	}
	var group *resolver.MultiResolverError
	if errors.As(err, &group) {
		t.Errorf("the halt carries %d grouped violations; one document's text is one fault and "+
			"is reported once", len(group.Violations))
	}
}

// TestASubAggregateWhoseTextIsNotATemplateStillHalts is the regression the
// retained raise exists to prevent.
//
// A module the composition imports is read from the catalog while the
// composition is assembled, so no authored document is ever presented for it and
// the declared rule is never asked about its text. Only the raise inside the
// render covers it. Removing that raise alongside the rule would leave this fault
// unreported, and nothing else in the tree would fail.
//
// What the halt is read through here is the chain rather than the outermost
// frame, and the distinction is the whole reason this proof reads the way it
// does. A halt reported from inside the composition is wrapped on its way out by
// the frame that was assembling it, and that frame's own text names the import it
// was following rather than this condition. The identity, the module and the
// parse detail all travel underneath it — which is what a reader is shown, since
// the diagnostic head takes its code from the same chain walk asserted below.
func TestASubAggregateWhoseTextIsNotATemplateStillHalts(t *testing.T) {
	t.Parallel()

	catalogPath := catalogWithPlantedModule(t)
	_, control := faultedRootAggregate(t)

	_, err := compileAggregateBytesWithCatalog(t, string(control), catalogPath)
	if err == nil {
		t.Fatal("a module whose text is not a template compiled without halting")
	}
	if !errors.Is(err, resolver.ErrInvalidParameterRef()) {
		t.Fatalf("the halt does not carry the invalid-parameter-reference condition: %v", err)
	}
	// The code a reader is shown for this halt. It is this condition's own,
	// because the frames wrapping it carry no identity of their own, and asserting
	// it is what would fail if one of them ever took one — at which point the
	// reader would be shown a different condition's code for this fault.
	short, ok := cerrors.ShortCodeOf(err)
	if !ok || short != "E0158" {
		t.Errorf("short code = %q (present %t), want %q", short, ok, "E0158")
	}
	// What pins this halt to the imported document rather than to the one the
	// build started from: the module is named, and the parse detail is the one
	// taken over its text. Asserting the condition alone would pass on a halt
	// raised over the root, and the two are exactly what this proof separates.
	chain := cerrors.CauseChain(err)
	for _, want := range []string{"db-module", "template parse error"} {
		if !strings.Contains(chain, want) {
			t.Errorf("the halt does not carry %q, so it is not pinned to the imported "+
				"document: %q", want, chain)
		}
	}
	t.Logf("halt, with its causes: %s", chain)
}

// catalogWithPlantedModule copies the fixture catalog into the test's own
// directory and appends the plant to the module the fixture Aggregate imports,
// returning the copy's path.
//
// The copy exists because the committed catalog is shared by every other proof in
// this package, so the fault has to live somewhere only this test reads.
func catalogWithPlantedModule(t *testing.T) string {
	t.Helper()

	dst := filepath.Join(t.TempDir(), "catalog")
	if err := os.CopyFS(dst, os.DirFS(invocationCatalogPath)); err != nil {
		t.Fatalf("copy the fixture catalog: %v", err)
	}
	module := filepath.Join(dst, "base", "aggregates", "db-module.yaml")
	text, err := os.ReadFile(module)
	if err != nil {
		t.Fatalf("read the copied module: %v", err)
	}
	if err := os.WriteFile(module, append(text, []byte(unterminatedAction)...), 0o644); err != nil {
		t.Fatalf("plant the fault in the copied module: %v", err)
	}
	return dst
}

// compileAggregateBytesWithCatalog drives the real pre-render pipeline over the
// fixture workspace with the supplied Aggregate and a caller-chosen catalog, and
// hands the caller the error rather than failing on it.
//
// It is the catalog-parameterised form of compileAggregateBytes and is written
// beside it rather than replacing it: every other proof in this package compiles
// against the committed catalog, and widening the shared helper's signature would
// make each of them state a path it does not care about.
func compileAggregateBytesWithCatalog(
	t *testing.T,
	aggregate string,
	catalogPath string,
) (*stepsOneToFourOutput, error) {
	t.Helper()

	rel, err := resolver.LoadRelease(invocationReleasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}
	manifest, err := resolver.LoadManifest(invocationManifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	cat, err := catalog.LoadAll("", catalogPath)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d error(s); first: %v", len(extErrs), extErrs[0])
	}
	var captured *stepsOneToFourOutput
	runErr := runStepsOneToFour(
		context.Background(),
		Inputs{
			Release:           rel,
			Manifest:          manifest,
			AggregateRawBytes: []byte(aggregate),
			AggregatePath:     invocationAggregatePath,
			ReleasePath:       invocationReleasePath,
			WorkspaceConfig:   defaultWorkspaceConfigForTest(),
			PluginPins:        pluginpath.NoPins(),
			CompilerVersion:   invocationVersion,
		},
		cat.CatalogLookup(),
		cat.RecipeLookupWithManifest(manifest).Recipe,
		cat.SubAggregateLoader(),
		func(out *stepsOneToFourOutput) error { captured = out; return nil },
	)
	return captured, runErr
}
