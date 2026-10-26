// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func mappingTierWith(typeEntries, nameEntries map[string]string, order []string) *types.MappingTier {
	t := &types.MappingTier{}
	for _, k := range order {
		if v, ok := typeEntries[k]; ok {
			t.Type.Set(k, v)
		}
		if v, ok := nameEntries[k]; ok {
			t.Name.Set(k, v)
		}
	}
	return t
}

// TestManifestRecipeReferenceFaults_AcceptsEveryShapeTheGrammarAllows pins the
// negative direction. Reporting a well-shaped reference would halt authoring the
// grammar explicitly accepts, which is worse than the misreport this reading
// exists to remove.
//
// It is also the control the positive assertions below need: a walk that yielded
// nothing and a document with nothing to report are indistinguishable from a
// clean result alone, so a green here means the rule ran only because its
// siblings prove it can refuse.
func TestManifestRecipeReferenceFaults_AcceptsEveryShapeTheGrammarAllows(t *testing.T) {
	t.Parallel()
	order := []string{"a", "b", "c", "d"}
	m := &types.Manifest{}
	m.Spec.Mappings.Components = mappingTierWith(map[string]string{
		"a": "mariadb",
		"b": "mariadb:1.0.0",
		"c": "helm/mariadb",
		"d": "helm/mariadb:1.0.0",
	}, nil, order)

	if faults := ManifestRecipeReferenceFaults(m); len(faults) != 0 {
		t.Fatalf("well-shaped references reported %d fault(s): %v", len(faults), faults)
	}
}

// TestManifestRecipeReferenceFaults_ReportsEveryMisShapedValue asserts the
// enumeration: one fault per offending value, over both axes of both mapping
// tiers, so a Manifest authoring several of them does not have all but one
// hidden.
//
// All four coordinates are driven rather than argued. The rule is invoked from
// one traversal over one entry list, so a tier or an axis dropped from the
// presentation would report nothing and read exactly like a Manifest that
// declared none.
func TestManifestRecipeReferenceFaults_ReportsEveryMisShapedValue(t *testing.T) {
	t.Parallel()
	m := &types.Manifest{}
	m.Spec.Mappings.Components = mappingTierWith(
		map[string]string{"core/compute/api": "core/helm/api-server:1.0.0"},
		map[string]string{"web": "a/b/c"},
		[]string{"core/compute/api", "web"},
	)
	m.Spec.Mappings.Features = mappingTierWith(
		map[string]string{"core/dns": "x/y/z:2.0.0"}, nil, []string{"core/dns"},
	)
	m.Spec.Mappings.Features.Name.Set("logs", "p/q/r")

	faults := ManifestRecipeReferenceFaults(m)
	if len(faults) != 4 {
		t.Fatalf("reported %d fault(s), want 4 (one per mis-shaped value): %v", len(faults), faults)
	}
	for _, f := range faults {
		if !errors.Is(f, ErrMalformedRecipeReference()) {
			t.Errorf("fault does not wrap the shape sentinel: %v", f)
		}
	}
	// Each fault names its own coordinate, so an author with several of them can
	// tell which mapping entry each one is about.
	for _, want := range []string{
		"spec.mappings.components.type.core/compute/api",
		"spec.mappings.components.name.web",
		"spec.mappings.features.type.core/dns",
		"spec.mappings.features.name.logs",
	} {
		found := false
		for _, f := range faults {
			if strings.Contains(f.Error(), want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no fault names the coordinate %q; got %v", want, faults)
		}
	}
}

// TestManifestRecipeReferenceFaults_QuotesTheGrammarsSingleHome is the
// single-source guard. The accepted-shape statement and the segment ceiling have
// one home; a reading that restated either would go quietly false the day a
// publisher position enters the grammar. Asserting the message carries the
// constant's own text is what makes that impossible to miss.
func TestManifestRecipeReferenceFaults_QuotesTheGrammarsSingleHome(t *testing.T) {
	t.Parallel()
	m := &types.Manifest{}
	m.Spec.Mappings.Components = mappingTierWith(
		map[string]string{"core/compute/api": "a/b/c/d"}, nil, []string{"core/compute/api"},
	)
	faults := ManifestRecipeReferenceFaults(m)
	if len(faults) != 1 {
		t.Fatalf("want exactly one fault, got %d: %v", len(faults), faults)
	}
	msg := faults[0].Error()
	if !strings.Contains(msg, AcceptedRecipeReferenceShapes) {
		t.Errorf("fault does not quote the accepted-shape statement verbatim: %q", msg)
	}
	if !strings.Contains(msg, "4 slash-separated segments") {
		t.Errorf("fault does not report the observed segment count: %q", msg)
	}
}

// TestManifestRecipeReferenceFaults_NilInputs covers the two empty shapes a
// caller can hand it: no Manifest at all, and a Manifest declaring no mappings.
func TestManifestRecipeReferenceFaults_NilInputs(t *testing.T) {
	t.Parallel()
	if faults := ManifestRecipeReferenceFaults(nil); faults != nil {
		t.Errorf("nil Manifest reported %v", faults)
	}
	if faults := ManifestRecipeReferenceFaults(&types.Manifest{}); faults != nil {
		t.Errorf("empty Manifest reported %v", faults)
	}
}

// TestManifestRecipeReferenceFault_ReadsAsThePublishedReference pins the whole
// sentence rather than parts of it.
//
// The halting-conditions reference reproduces this message verbatim, so the text
// is a contract with a reader and not a presentation choice: a change to it that
// no gate catches leaves a published page quoting output the compiler no longer
// prints. Comparing the whole string is what makes the two move together.
func TestManifestRecipeReferenceFault_ReadsAsThePublishedReference(t *testing.T) {
	t.Parallel()
	m := &types.Manifest{}
	m.Spec.Mappings.Components = mappingTierWith(
		map[string]string{"core/compute/api": "core/helm/eso-aws-fetcher:1.0.0"},
		nil, []string{"core/compute/api"},
	)
	faults := ManifestRecipeReferenceFaults(m)
	if len(faults) != 1 {
		t.Fatalf("want exactly one fault, got %d: %v", len(faults), faults)
	}
	want := "malformed recipe reference in a Manifest mapping value: " +
		"spec.mappings.components.type.core/compute/api maps to " +
		`"core/helm/eso-aws-fetcher:1.0.0", which carries 3 slash-separated segments; ` +
		`a recipe reference accepts "name", "name:version", "capability/name" or ` +
		`"capability/name:version"; correct the mapping value in the Manifest`
	if got := faults[0].Error(); got != want {
		t.Errorf("published sentence changed.\n got: %s\nwant: %s", got, want)
	}
}

// TestManifestRecipeReferenceFault_FeatureTierNamesItsOwnReferenceKind pins the
// one word the two arms differ in.
//
// The tier is what tells an author whether the value has to name a Recipe or a
// FeatureRecipe, and it is the only thing in the sentence that says so — the
// coordinate names the block and the shapes are identical either way. A rule that
// reported both arms as the component kind would still name the right key and
// send the author to the wrong catalog surface.
func TestManifestRecipeReferenceFault_FeatureTierNamesItsOwnReferenceKind(t *testing.T) {
	t.Parallel()
	m := &types.Manifest{}
	m.Spec.Mappings.Features = mappingTierWith(
		map[string]string{"core/dns": "x/y/z:2.0.0"}, nil, []string{"core/dns"},
	)
	faults := ManifestRecipeReferenceFaults(m)
	if len(faults) != 1 {
		t.Fatalf("want exactly one fault, got %d: %v", len(faults), faults)
	}
	if got := faults[0].Error(); !strings.Contains(got, "a feature recipe reference accepts") {
		t.Errorf("the features arm does not name its own reference kind: %s", got)
	}
}

// TestManifestRecipeReferenceFaults_RefusesAVersionPinInTheCapabilitySegment
// drives the grammar's second fault: a pin belongs on the `capability` a recipe
// declares and is no part of the name a reference uses, so a mapping value
// spelling it into the capability segment is refused as mis-shaped — on both
// axes of both tiers, exact and caret alike. Each refused value carries that one
// fault; the controls beside them are well-shaped references a version colon
// appears in elsewhere, and a value at fault both ways is reported once, as its
// segment count.
func TestManifestRecipeReferenceFaults_RefusesAVersionPinInTheCapabilitySegment(t *testing.T) {
	t.Parallel()
	m := &types.Manifest{}
	m.Spec.Mappings.Components = mappingTierWith(
		map[string]string{
			"acme/a": "compose:1.2.3/svc:1.0.0",
			"acme/b": "compose/svc:1.0.0",
			"acme/c": "svc:1.0.0",
			"acme/d": "compose:1.2.3/x/y",
		},
		map[string]string{"web": "compose:^1.0.0/svc"},
		[]string{"acme/a", "acme/b", "acme/c", "acme/d", "web"},
	)
	m.Spec.Mappings.Features = mappingTierWith(
		map[string]string{"core/dns": "helm:2.0.0/dns-fr:1.0.0"}, nil, []string{"core/dns"},
	)

	faults := ManifestRecipeReferenceFaults(m)
	if len(faults) != 4 {
		t.Fatalf("reported %d fault(s), want 4 (three pinned values and one over the segment ceiling): %v",
			len(faults), faults)
	}
	for i, want := range []string{
		`spec.mappings.components.type.acme/a maps to "compose:1.2.3/svc:1.0.0", which carries a version pin in its capability segment "compose:1.2.3"`,
		`spec.mappings.components.type.acme/d maps to "compose:1.2.3/x/y", which carries 3 slash-separated segments`,
		`spec.mappings.components.name.web maps to "compose:^1.0.0/svc", which carries a version pin in its capability segment "compose:^1.0.0"`,
		`spec.mappings.features.type.core/dns maps to "helm:2.0.0/dns-fr:1.0.0", which carries a version pin in its capability segment "helm:2.0.0"; a pin belongs on the ` + "`capability`" + ` the recipe declares, and a feature recipe reference names the recipe by its bare capability`,
	} {
		if !errors.Is(faults[i], ErrMalformedRecipeReference()) {
			t.Errorf("fault %d does not wrap the shape condition: %v", i, faults[i])
		}
		if !strings.Contains(faults[i].Error(), want) {
			t.Errorf("fault %d, in the order the Manifest declares its entries, does not read %q:\n%s",
				i, want, faults[i])
		}
	}
}

// TestPinnedCapabilityReferenceFault_ReadsAsThePublishedReference pins the whole
// sentence, for the reason the segment sentence's own test gives.
func TestPinnedCapabilityReferenceFault_ReadsAsThePublishedReference(t *testing.T) {
	t.Parallel()
	m := &types.Manifest{}
	m.Spec.Mappings.Components = mappingTierWith(
		map[string]string{"acme/svc": "compose:1.2.3/svc:1.0.0"}, nil, []string{"acme/svc"},
	)
	faults := ManifestRecipeReferenceFaults(m)
	if len(faults) != 1 {
		t.Fatalf("want exactly one fault, got %d: %v", len(faults), faults)
	}
	want := "malformed recipe reference in a Manifest mapping value: " +
		`spec.mappings.components.type.acme/svc maps to "compose:1.2.3/svc:1.0.0", which carries ` +
		`a version pin in its capability segment "compose:1.2.3"; a pin belongs on the ` + "`capability`" +
		` the recipe declares, and a recipe reference names the recipe by its bare capability, as "name", ` +
		`"name:version", "capability/name" or "capability/name:version"; correct the mapping value in the Manifest`
	if got := faults[0].Error(); got != want {
		t.Errorf("published sentence changed.\n got: %s\nwant: %s", got, want)
	}
}

// TestRecipeReferenceCapabilityPin pins the predicate every reader of a
// reference's shape shares: only a version colon in the segment before the first
// slash is a pinned capability.
func TestRecipeReferenceCapabilityPin(t *testing.T) {
	t.Parallel()
	for _, r := range []struct {
		uri     string
		segment string
		pinned  bool
	}{
		{"compose:1.2.3/svc:1.0.0", "compose:1.2.3", true},
		{"compose:^1.0.0/svc", "compose:^1.0.0", true},
		{"compose/svc:1.0.0", "compose", false},
		{"svc:1.0.0", "", false},
		{"svc", "", false},
	} {
		segment, pinned := RecipeReferenceCapabilityPin(r.uri)
		if pinned != r.pinned || (pinned && segment != r.segment) {
			t.Errorf("RecipeReferenceCapabilityPin(%q) = (%q, %v); want (%q, %v)", r.uri, segment, pinned, r.segment, r.pinned)
		}
		if got := RecipeReferenceMisShaped(r.uri); got != r.pinned {
			t.Errorf("RecipeReferenceMisShaped(%q) = %v; want %v", r.uri, got, r.pinned)
		}
	}
}

// TestManifestRecipeReferenceShapeRule_PositionsAtTheAuthoredEntry asserts the
// framework supplied the position, which the composed sentence cannot show.
//
// The sentence names the coordinate because the conversion reads it off the
// locator, so asserting the sentence alone would pass on a rule that invented the
// coordinate and carried no locator at all — and a consumer that positions a
// finding reads the locator rather than the words. This drives the rule through
// its own traversal and reads the objection directly.
func TestManifestRecipeReferenceShapeRule_PositionsAtTheAuthoredEntry(t *testing.T) {
	t.Parallel()
	m := &types.Manifest{}
	m.Spec.Mappings.Components = mappingTierWith(
		nil, map[string]string{"web": "a/b/c"}, []string{"web"},
	)
	var presented constraint.DocumentView = newManifestDocumentView("", m)
	objections := constraint.RunOnDocumentMappingEntries(
		"",
		presented.ManifestRecipeMappingEntries(""),
		presented,
		manifestRecipeReferenceIsWellShaped,
	)
	if len(objections) != 1 {
		t.Fatalf("want exactly one objection, got %d", len(objections))
	}
	if got, want := objections[0].Locator(), ".spec.mappings.components.name.web"; got != want {
		t.Errorf("locator = %q, want %q", got, want)
	}
	if got := objections[0].SourceFile(); got != "" {
		t.Errorf("a composed Manifest has no single source file; got %q", got)
	}
}
